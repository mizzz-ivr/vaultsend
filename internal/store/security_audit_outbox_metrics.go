package store

import "context"

// SecurityAuditOutboxMetrics は監査outboxの運用監視に必要な集約値を保持する。
type SecurityAuditOutboxMetrics struct {
	PendingCount                  int64
	OldestPendingAgeSeconds       float64
	OldestPendingCreatedTimestamp float64
}

// GetSecurityAuditOutboxMetrics は未処理件数と最古未処理行の経過時間を1回のSQLで取得する。
func (q *Queries) GetSecurityAuditOutboxMetrics(ctx context.Context) (SecurityAuditOutboxMetrics, error) {
	const query = `
SELECT
    COUNT(*)::bigint,
    COALESCE(
        GREATEST(EXTRACT(EPOCH FROM (now() - MIN(created_at))), 0),
        0
    )::double precision,
    COALESCE(EXTRACT(EPOCH FROM MIN(created_at)), 0)::double precision
FROM security_audit_outbox
WHERE processed_at IS NULL`

	var metrics SecurityAuditOutboxMetrics
	if err := q.db.QueryRow(ctx, query).Scan(
		&metrics.PendingCount,
		&metrics.OldestPendingAgeSeconds,
		&metrics.OldestPendingCreatedTimestamp,
	); err != nil {
		return SecurityAuditOutboxMetrics{}, err
	}
	return metrics, nil
}
