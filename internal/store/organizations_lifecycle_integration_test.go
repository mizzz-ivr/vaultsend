//go:build integration

package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOrganizationLifecycleIntegration(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL が未設定のためintegration testをスキップします")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("PostgreSQL poolの作成に失敗しました: %v", err)
	}
	t.Cleanup(pool.Close)

	queries := New(pool)
	ownerID := uuid.New()
	targetID := uuid.New()
	memberID := uuid.New()
	strangerID := uuid.New()
	userIDs := []uuid.UUID{ownerID, targetID, memberID, strangerID}
	for _, userID := range userIDs {
		email := "org-lifecycle-" + userID.String() + "@example.com"
		if _, err := pool.Exec(ctx, `
INSERT INTO users (id, email, email_normalized, password_hash, status)
VALUES ($1,$2,$2,'integration-password-hash','active')`, userID, email); err != nil {
			t.Fatalf("test userの作成に失敗しました: %v", err)
		}
	}

	org, err := queries.CreateOrg(ctx, CreateOrgParams{Name: "Lifecycle integration", OwnerUserID: ownerID})
	if err != nil {
		t.Fatalf("organizationの作成に失敗しました: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, org.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=ANY($1::uuid[])`, userIDs)
	})

	if _, err := queries.AddMember(ctx, org.ID, targetID, "admin"); err != nil {
		t.Fatalf("target memberの追加に失敗しました: %v", err)
	}
	if _, err := queries.AddMember(ctx, org.ID, memberID, "member"); err != nil {
		t.Fatalf("memberの追加に失敗しました: %v", err)
	}

	updated, err := queries.UpdateOrganizationName(ctx, org.ID, "Lifecycle renamed")
	if err != nil || updated.Name != "Lifecycle renamed" {
		t.Fatalf("organization rename mismatch: org=%#v err=%v", updated, err)
	}

	transferred, err := queries.TransferOrganizationOwnership(ctx, org.ID, ownerID, targetID)
	if err != nil {
		t.Fatalf("owner transferに失敗しました: %v", err)
	}
	if transferred.OwnerUserID != targetID {
		t.Fatalf("owner_user_id mismatch: got=%s want=%s", transferred.OwnerUserID, targetID)
	}
	oldOwnerMember, err := queries.GetOrganizationMember(ctx, org.ID, ownerID)
	if err != nil || oldOwnerMember.Role != "admin" {
		t.Fatalf("旧owner role mismatch: member=%#v err=%v", oldOwnerMember, err)
	}
	newOwnerMember, err := queries.GetOrganizationMember(ctx, org.ID, targetID)
	if err != nil || newOwnerMember.Role != "owner" {
		t.Fatalf("新owner role mismatch: member=%#v err=%v", newOwnerMember, err)
	}

	if err := queries.RemoveMember(ctx, org.ID, targetID); !errors.Is(err, ErrConflict) {
		t.Fatalf("current owner削除でErrConflictを期待しました: %v", err)
	}
	if err := queries.LeaveOrganization(ctx, org.ID, targetID); !errors.Is(err, ErrConflict) {
		t.Fatalf("current owner退出でErrConflictを期待しました: %v", err)
	}
	if err := queries.LeaveOrganization(ctx, org.ID, ownerID); err != nil {
		t.Fatalf("旧owner(admin)の退出に失敗しました: %v", err)
	}
	if _, err := queries.GetOrganizationMember(ctx, org.ID, ownerID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("退出後の旧ownerが残っています: %v", err)
	}

	if _, err := queries.AddMember(ctx, org.ID, strangerID, "owner"); !errors.Is(err, ErrConflict) {
		t.Fatalf("2人目ownerの追加でErrConflictを期待しました: %v", err)
	}
}
