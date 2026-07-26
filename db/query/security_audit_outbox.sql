-- name: CreateSecurityAuditOutboxEvent :exec
INSERT INTO security_audit_outbox (
    id, occurred_at, event_type, severity, outcome, actor_type, actor_user_id,
    organization_id, resource_type, resource_id, request_id, source_service,
    http_method, route_pattern, status_code, client_ip_hmac, user_agent_hmac,
    details, integrity_key_id, integrity_hmac
) VALUES (
    $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20
);

-- name: CountPendingSecurityAuditOutbox :one
SELECT COUNT(*)
FROM security_audit_outbox
WHERE processed_at IS NULL;

-- name: DeleteProcessedSecurityAuditOutboxBefore :execrows
WITH targets AS (
    SELECT id
    FROM security_audit_outbox
    WHERE processed_at IS NOT NULL
      AND processed_at < $1
    ORDER BY processed_at ASC, id ASC
    LIMIT $2
)
DELETE FROM security_audit_outbox AS outbox
USING targets
WHERE outbox.id = targets.id;
