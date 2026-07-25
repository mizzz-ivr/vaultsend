-- name: CreateSecurityAuditEvent :one
INSERT INTO security_audit_events (
    id, occurred_at, event_type, severity, outcome, actor_type, actor_user_id,
    organization_id, resource_type, resource_id, request_id, source_service,
    http_method, route_pattern, status_code, client_ip_hmac, user_agent_hmac,
    details, integrity_key_id, integrity_hmac
) VALUES (
    $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18::jsonb,$19,$20
)
RETURNING *;

-- name: ListSecurityAuditEventsByOrganization :many
SELECT *
FROM security_audit_events
WHERE organization_id = $1
ORDER BY occurred_at DESC, id DESC
LIMIT $2 OFFSET $3;

-- name: CountSecurityAuditEventsByOrganization :one
SELECT COUNT(1)
FROM security_audit_events
WHERE organization_id = $1;
