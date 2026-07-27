package store

import (
	"context"

	"github.com/google/uuid"
)

type securityAuditOutboxContextKey struct{}

// SecurityAuditOutboxEvent は業務Storeが監査イベントへ補完する操作固有情報を表す。
type SecurityAuditOutboxEvent struct {
	EventType      string
	Severity       string
	Outcome        string
	ActorType      string
	ActorUserID    *uuid.UUID
	OrganizationID *uuid.UUID
	ResourceType   string
	ResourceID     *uuid.UUID
	StatusCode     int
	Details        map[string]string
}

// SecurityAuditOutboxState はHTTP境界で生成した署名済みイベント作成関数と配送状態を保持する。
type SecurityAuditOutboxState struct {
	Prepare  func(SecurityAuditOutboxEvent) (CreateSecurityAuditEventParams, error)
	Enqueued bool
}

func WithSecurityAuditOutboxState(ctx context.Context, state *SecurityAuditOutboxState) context.Context {
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, securityAuditOutboxContextKey{}, state)
}

func PrepareSecurityAuditOutboxEvent(ctx context.Context, event SecurityAuditOutboxEvent) (CreateSecurityAuditEventParams, bool, error) {
	state := securityAuditOutboxStateFromContext(ctx)
	if state == nil || state.Prepare == nil {
		return CreateSecurityAuditEventParams{}, false, nil
	}
	prepared, err := state.Prepare(event)
	if err != nil {
		return CreateSecurityAuditEventParams{}, true, err
	}
	return prepared, true, nil
}

func MarkSecurityAuditOutboxEnqueued(ctx context.Context) {
	if state := securityAuditOutboxStateFromContext(ctx); state != nil {
		state.Enqueued = true
	}
}

func SecurityAuditOutboxEnqueued(ctx context.Context) bool {
	state := securityAuditOutboxStateFromContext(ctx)
	return state != nil && state.Enqueued
}

func securityAuditOutboxStateFromContext(ctx context.Context) *SecurityAuditOutboxState {
	state, _ := ctx.Value(securityAuditOutboxContextKey{}).(*SecurityAuditOutboxState)
	return state
}
