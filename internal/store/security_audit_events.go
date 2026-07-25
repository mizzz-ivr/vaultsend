package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type SecurityAuditEvent struct {
	ID               uuid.UUID
	OccurredAt       time.Time
	RecordedAt       time.Time
	EventType        string
	Severity         string
	Outcome          string
	ActorType        string
	ActorUserID      *uuid.UUID
	OrganizationID   *uuid.UUID
	ResourceType     *string
	ResourceID       *uuid.UUID
	RequestID        *string
	SourceService    string
	HTTPMethod       *string
	RoutePattern     *string
	StatusCode       *int32
	ClientIPHMAC     *string
	UserAgentHMAC    *string
	Details          json.RawMessage
	IntegrityKeyID   string
	IntegrityHMAC    string
}

type CreateSecurityAuditEventParams struct {
	ID               uuid.UUID
	OccurredAt       time.Time
	EventType        string
	Severity         string
	Outcome          string
	ActorType        string
	ActorUserID      *uuid.UUID
	OrganizationID   *uuid.UUID
	ResourceType     *string
	ResourceID       *uuid.UUID
	RequestID        *string
	SourceService    string
	HTTPMethod       *string
	RoutePattern     *string
	StatusCode       *int32
	ClientIPHMAC     *string
	UserAgentHMAC    *string
	Details          json.RawMessage
	IntegrityKeyID   string
	IntegrityHMAC    string
}

type securityAuditDBTX interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (q *Queries) CreateSecurityAuditEvent(ctx context.Context, arg CreateSecurityAuditEventParams) (SecurityAuditEvent, error) {
	return q.createSecurityAuditEvent(ctx, q.db, arg)
}

func (q *Queries) createSecurityAuditEvent(ctx context.Context, db securityAuditDBTX, arg CreateSecurityAuditEventParams) (SecurityAuditEvent, error) {
	const query = `
INSERT INTO security_audit_events (
    id, occurred_at, event_type, severity, outcome, actor_type, actor_user_id,
    organization_id, resource_type, resource_id, request_id, source_service,
    http_method, route_pattern, status_code, client_ip_hmac, user_agent_hmac,
    details, integrity_key_id, integrity_hmac
) VALUES (
    $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18::jsonb,$19,$20
)
RETURNING id, occurred_at, recorded_at, event_type, severity, outcome, actor_type,
          actor_user_id, organization_id, resource_type, resource_id, request_id,
          source_service, http_method, route_pattern, status_code, client_ip_hmac,
          user_agent_hmac, details, integrity_key_id, integrity_hmac`

	details := arg.Details
	if len(details) == 0 {
		details = json.RawMessage(`{}`)
	}
	var event SecurityAuditEvent
	err := scanSecurityAuditEvent(db.QueryRow(
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
	), &event)
	return event, err
}

func (q *Queries) ListSecurityAuditEventsByOrganization(ctx context.Context, organizationID uuid.UUID, limit, offset int32) ([]SecurityAuditEvent, error) {
	const query = `
SELECT id, occurred_at, recorded_at, event_type, severity, outcome, actor_type,
       actor_user_id, organization_id, resource_type, resource_id, request_id,
       source_service, http_method, route_pattern, status_code, client_ip_hmac,
       user_agent_hmac, details, integrity_key_id, integrity_hmac
FROM security_audit_events
WHERE organization_id = $1
ORDER BY occurred_at DESC, id DESC
LIMIT $2 OFFSET $3`

	rows, err := q.db.Query(ctx, query, organizationID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]SecurityAuditEvent, 0, limit)
	for rows.Next() {
		var event SecurityAuditEvent
		if err := scanSecurityAuditEvent(rows, &event); err != nil {
			return nil, err
		}
		items = append(items, event)
	}
	return items, rows.Err()
}

func (q *Queries) CountSecurityAuditEventsByOrganization(ctx context.Context, organizationID uuid.UUID) (int64, error) {
	const query = `SELECT COUNT(1) FROM security_audit_events WHERE organization_id = $1`
	var total int64
	if err := q.db.QueryRow(ctx, query, organizationID).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func scanSecurityAuditEvent(row pgx.Row, event *SecurityAuditEvent) error {
	return row.Scan(
		&event.ID,
		&event.OccurredAt,
		&event.RecordedAt,
		&event.EventType,
		&event.Severity,
		&event.Outcome,
		&event.ActorType,
		&event.ActorUserID,
		&event.OrganizationID,
		&event.ResourceType,
		&event.ResourceID,
		&event.RequestID,
		&event.SourceService,
		&event.HTTPMethod,
		&event.RoutePattern,
		&event.StatusCode,
		&event.ClientIPHMAC,
		&event.UserAgentHMAC,
		&event.Details,
		&event.IntegrityKeyID,
		&event.IntegrityHMAC,
	)
}
