package store

import (
	"context"
	"encoding/json"
	"time"
)

func createSecurityAuditOutboxEvent(ctx context.Context, db dbtx, arg CreateSecurityAuditEventParams) error {
	const query = `
INSERT INTO security_audit_outbox (
    id, occurred_at, event_type, severity, outcome, actor_type, actor_user_id,
    organization_id, resource_type, resource_id, request_id, source_service,
    http_method, route_pattern, status_code, client_ip_hmac, user_agent_hmac,
    details, integrity_key_id, integrity_hmac
) VALUES (
    $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18::jsonb,$19,$20
)`
	details := arg.Details
	if len(details) == 0 {
		details = json.RawMessage(`{}`)
	}
	_, err := db.Exec(
		ctx,
		query,
		arg.ID,
		arg.OccurredAt,
		arg.EventType,
		arg.Severity,
		arg.Outcome,
		arg.ActorType,
		arg.ActorUserID,
		arg.OrganizationID,
		arg.ResourceType,
		arg.ResourceID,
		arg.RequestID,
		arg.SourceService,
		arg.HTTPMethod,
		arg.RoutePattern,
		arg.StatusCode,
		arg.ClientIPHMAC,
		arg.UserAgentHMAC,
		string(details),
		arg.IntegrityKeyID,
		arg.IntegrityHMAC,
	)
	return err
}

// DeliverSecurityAuditOutboxBatch は未処理行を排他取得し、監査ログへのINSERTと処理済み更新を同一transactionで行う。
func (q *Queries) DeliverSecurityAuditOutboxBatch(ctx context.Context, limit int32) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	const query = `
WITH claimed AS MATERIALIZED (
    SELECT id, occurred_at, event_type, severity, outcome, actor_type, actor_user_id,
           organization_id, resource_type, resource_id, request_id, source_service,
           http_method, route_pattern, status_code, client_ip_hmac, user_agent_hmac,
           details, integrity_key_id, integrity_hmac
    FROM security_audit_outbox
    WHERE processed_at IS NULL
      AND available_at <= now()
    ORDER BY available_at ASC, created_at ASC, id ASC
    FOR UPDATE SKIP LOCKED
    LIMIT $1
), inserted AS (
    INSERT INTO security_audit_events (
        id, occurred_at, event_type, severity, outcome, actor_type, actor_user_id,
        organization_id, resource_type, resource_id, request_id, source_service,
        http_method, route_pattern, status_code, client_ip_hmac, user_agent_hmac,
        details, integrity_key_id, integrity_hmac
    )
    SELECT id, occurred_at, event_type, severity, outcome, actor_type, actor_user_id,
           organization_id, resource_type, resource_id, request_id, source_service,
           http_method, route_pattern, status_code, client_ip_hmac, user_agent_hmac,
           details, integrity_key_id, integrity_hmac
    FROM claimed
    ON CONFLICT (id) DO NOTHING
    RETURNING id
), marked AS (
    UPDATE security_audit_outbox AS outbox
    SET processed_at = now()
    FROM claimed
    WHERE outbox.id = claimed.id
      AND outbox.processed_at IS NULL
      AND (SELECT COUNT(*) FROM inserted) >= 0
    RETURNING outbox.id
)
SELECT COUNT(*) FROM marked`
	var delivered int64
	if err := q.db.QueryRow(ctx, query, limit).Scan(&delivered); err != nil {
		return 0, err
	}
	return delivered, nil
}

func (q *Queries) DeleteProcessedSecurityAuditOutboxBefore(ctx context.Context, before time.Time, limit int32) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	const query = `
WITH targets AS (
    SELECT id
    FROM security_audit_outbox
    WHERE processed_at IS NOT NULL
      AND processed_at < $1
    ORDER BY processed_at ASC, id ASC
    LIMIT $2
), deleted AS (
    DELETE FROM security_audit_outbox AS outbox
    USING targets
    WHERE outbox.id = targets.id
    RETURNING outbox.id
)
SELECT COUNT(*) FROM deleted`
	var deleted int64
	if err := q.db.QueryRow(ctx, query, before, limit).Scan(&deleted); err != nil {
		return 0, err
	}
	return deleted, nil
}

func (q *Queries) CountPendingSecurityAuditOutbox(ctx context.Context) (int64, error) {
	const query = `SELECT COUNT(*) FROM security_audit_outbox WHERE processed_at IS NULL`
	var count int64
	if err := q.db.QueryRow(ctx, query).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
