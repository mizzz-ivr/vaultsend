//go:build integration

package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSecurityAuditEventsAreAppendOnly(t *testing.T) {
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

	assertRejected := func(t *testing.T, mutation func(context.Context, pgx.Tx, uuid.UUID) error) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("transaction開始に失敗しました: %v", err)
		}
		defer tx.Rollback(ctx)
		eventID := uuid.New()
		_, err = queries.createSecurityAuditEvent(ctx, tx, CreateSecurityAuditEventParams{
			ID:             eventID,
			OccurredAt:     time.Now().UTC().Truncate(time.Microsecond),
			EventType:      "security.audit.integration_test",
			Severity:       "info",
			Outcome:        "success",
			ActorType:      "system",
			SourceService:  "api",
			Details:        json.RawMessage(`{"schema_version":"1"}`),
			IntegrityKeyID: "integration-v1",
			IntegrityHMAC:  strings.Repeat("a", 64),
		})
		if err != nil {
			t.Fatalf("監査イベントの作成に失敗しました: %v", err)
		}
		err = mutation(ctx, tx, eventID)
		if err == nil {
			t.Fatal("追記専用制約を回避して監査イベントを変更できました")
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55000" {
			t.Fatalf("SQLSTATE 55000を期待しました: %v", err)
		}
	}

	t.Run("UPDATEを拒否する", func(t *testing.T) {
		assertRejected(t, func(ctx context.Context, tx pgx.Tx, eventID uuid.UUID) error {
			_, err := tx.Exec(ctx, `UPDATE security_audit_events SET severity='warning' WHERE id=$1`, eventID)
			return err
		})
	})
	t.Run("DELETEを拒否する", func(t *testing.T) {
		assertRejected(t, func(ctx context.Context, tx pgx.Tx, eventID uuid.UUID) error {
			_, err := tx.Exec(ctx, `DELETE FROM security_audit_events WHERE id=$1`, eventID)
			return err
		})
	})
	t.Run("TRUNCATEを拒否する", func(t *testing.T) {
		assertRejected(t, func(ctx context.Context, tx pgx.Tx, _ uuid.UUID) error {
			_, err := tx.Exec(ctx, `TRUNCATE TABLE security_audit_events`)
			return err
		})
	})
}
