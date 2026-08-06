//go:build integration

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOrganizationInvitationLifecycleIntegration(t *testing.T) {
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
	inviteeID := uuid.New()
	ownerEmail := "invite-owner-" + uuid.NewString() + "@example.com"
	inviteeEmail := "invite-member-" + uuid.NewString() + "@example.com"
	for _, user := range []struct {
		id    uuid.UUID
		email string
	}{{ownerID, ownerEmail}, {inviteeID, inviteeEmail}} {
		if _, err := pool.Exec(ctx, `
INSERT INTO users (id, email, email_normalized, password_hash, status)
VALUES ($1,$2,$3,'integration-password-hash','active')`, user.id, user.email, user.email); err != nil {
			t.Fatalf("test userの作成に失敗しました: %v", err)
		}
	}

	org, err := queries.CreateOrg(ctx, CreateOrgParams{Name: "招待integration", OwnerUserID: ownerID})
	if err != nil {
		t.Fatalf("organizationの作成に失敗しました: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM organizations WHERE id=$1`, org.ID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=ANY($1::uuid[])`, []uuid.UUID{ownerID, inviteeID})
	})

	plainToken := "integration-invitation-token"
	hash := sha256.Sum256([]byte(plainToken))
	tokenHash := hex.EncodeToString(hash[:])
	expiresAt := time.Now().UTC().Add(7 * 24 * time.Hour)
	invitation, err := queries.CreateOrganizationInvitation(ctx, CreateOrganizationInvitationParams{
		OrganizationID:  org.ID,
		Email:           inviteeEmail,
		EmailNormalized: inviteeEmail,
		Role:            "member",
		TokenHash:       tokenHash,
		InvitedByUserID: ownerID,
		ExpiresAt:       expiresAt,
	})
	if err != nil {
		t.Fatalf("invitationの作成に失敗しました: %v", err)
	}

	_, err = queries.CreateOrganizationInvitation(ctx, CreateOrganizationInvitationParams{
		OrganizationID:  org.ID,
		Email:           inviteeEmail,
		EmailNormalized: inviteeEmail,
		Role:            "admin",
		TokenHash:       hex.EncodeToString(sha256.New().Sum([]byte("duplicate"))),
		InvitedByUserID: ownerID,
		ExpiresAt:       expiresAt,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("有効な重複招待でErrConflictを期待しました: %v", err)
	}

	stored, err := queries.GetOrganizationInvitationByTokenHash(ctx, tokenHash)
	if err != nil || stored.ID != invitation.ID || stored.TokenHash != tokenHash {
		t.Fatalf("token hashによる取得に失敗しました: invitation=%#v err=%v", stored, err)
	}
	pending, err := queries.CountPendingOrganizationInvitations(ctx, org.ID)
	if err != nil || pending != 1 {
		t.Fatalf("pending count mismatch: count=%d err=%v", pending, err)
	}

	member, err := queries.AcceptOrganizationInvitation(ctx, AcceptOrganizationInvitationParams{
		InvitationID: invitation.ID,
		TokenHash:    tokenHash,
		UserID:       inviteeID,
	})
	if err != nil {
		t.Fatalf("invitationの承認に失敗しました: %v", err)
	}
	if member.OrganizationID != org.ID || member.UserID != inviteeID || member.Role != "member" {
		t.Fatalf("member mismatch: %#v", member)
	}

	accepted, err := queries.GetOrganizationInvitationByID(ctx, org.ID, invitation.ID)
	if err != nil || accepted.Status != "accepted" || accepted.AcceptedByUserID == nil || *accepted.AcceptedByUserID != inviteeID {
		t.Fatalf("accepted invitation mismatch: %#v err=%v", accepted, err)
	}
	if _, err := queries.AcceptOrganizationInvitation(ctx, AcceptOrganizationInvitationParams{
		InvitationID: invitation.ID,
		TokenHash:    tokenHash,
		UserID:       inviteeID,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("二重承認でErrConflictを期待しました: %v", err)
	}
}
