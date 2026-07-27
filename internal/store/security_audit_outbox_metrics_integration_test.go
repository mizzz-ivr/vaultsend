//go:build integration

package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGetSecurityAuditOutboxMetrics(t *testing.T) {
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

	baseline, err := queries.GetSecurityAuditOutboxMetrics(ctx)
	if err != nil {
		t.Fatalf("baseline metricsの取得に失敗しました: %v", err)
	}
	if baseline.PendingCount != 0 {
		t.Fatalf("テスト開始時に未処理outboxが残っています: %d", baseline.PendingCount)
	}

	oldestID := uuid.New()
	newestID := uuid.New()
	oldestCreatedAt := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Microsecond)
	newestCreatedAt := time.Now().UTC().Add(-30 * time.Second).Truncate(time.Microsecond)
	insertAuditMetricsTestOutbox(t, ctx, pool, oldestID, oldestCreatedAt)
	insertAuditMetricsTestOutbox(t, ctx, pool, newestID, newestCreatedAt)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM security_audit_outbox WHERE id = ANY($1::uuid[])`, []uuid.UUID{oldestID, newestID})
	})

	metrics, err := queries.GetSecurityAuditOutboxMetrics(ctx)
	if err != nil {
		t.Fatalf("metricsの取得に失敗しました: %v", err)
	}
	if metrics.PendingCount != 2 {
		t.Fatalf("pending件数が不正です: got=%d want=2", metrics.PendingCount)
	}
	if metrics.OldestPendingAgeSeconds < 110 || metrics.OldestPendingAgeSeconds > 180 {
		t.Fatalf("最古pending経過秒が範囲外です: %f", metrics.OldestPendingAgeSeconds)
	}
	if diff := metrics.OldestPendingCreatedTimestamp - float64(oldestCreatedAt.Unix()); diff < -1 || diff > 1 {
		t.Fatalf("最古pending作成時刻が不正です: got=%f want=%d", metrics.OldestPendingCreatedTimestamp, oldestCreatedAt.Unix())
	}

	if _, err := pool.Exec(ctx, `UPDATE security_audit_outbox SET processed_at = now() WHERE id = $1`, oldestID); err != nil {
		t.Fatalf("最古outboxの処理済み更新に失敗しました: %v", err)
	}
	metrics, err = queries.GetSecurityAuditOutboxMetrics(ctx)
	if err != nil {
		t.Fatalf("1件処理後metricsの取得に失敗しました: %v", err)
	}
	if metrics.PendingCount != 1 {
		t.Fatalf("1件処理後pending件数が不正です: got=%d want=1", metrics.PendingCount)
	}
	if metrics.OldestPendingAgeSeconds < 20 || metrics.OldestPendingAgeSeconds > 90 {
		t.Fatalf("1件処理後の最古pending経過秒が範囲外です: %f", metrics.OldestPendingAgeSeconds)
	}
	if diff := metrics.OldestPendingCreatedTimestamp - float64(newestCreatedAt.Unix()); diff < -1 || diff > 1 {
		t.Fatalf("1件処理後の最古pending作成時刻が不正です: got=%f want=%d", metrics.OldestPendingCreatedTimestamp, newestCreatedAt.Unix())
	}

	if _, err := pool.Exec(ctx, `UPDATE security_audit_outbox SET processed_at = now() WHERE id = $1`, newestID); err != nil {
		t.Fatalf("残りoutboxの処理済み更新に失敗しました: %v", err)
	}
	metrics, err = queries.GetSecurityAuditOutboxMetrics(ctx)
	if err != nil {
		t.Fatalf("全件処理後metricsの取得に失敗しました: %v", err)
	}
	if metrics.PendingCount != 0 || metrics.OldestPendingAgeSeconds != 0 || metrics.OldestPendingCreatedTimestamp != 0 {
		t.Fatalf("全件処理後metricsが0ではありません: %+v", metrics)
	}
}

func insertAuditMetricsTestOutbox(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, createdAt time.Time) {
	t.Helper()
	_, err := pool.Exec(ctx, `
INSERT INTO security_audit_outbox (
    id, occurred_at, event_type, severity, outcome, actor_type,
    source_service, details, integrity_key_id, integrity_hmac,
    available_at, created_at
) VALUES (
    $1, $2, 'security.audit.metrics.test', 'info', 'success', 'system',
    'api', '{}'::jsonb, 'integration-test', $3,
    $2, $2
)`, id, createdAt, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("監査outboxテストデータの作成に失敗しました: %v", err)
	}
}
