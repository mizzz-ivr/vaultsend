package middleware

import (
	"context"
	"net/http"
	"net/netip"
	"strings"

	"github.com/example/vaultsend/internal/service"
	"github.com/example/vaultsend/internal/store"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

type outboxAwareSecurityAuditRecorder struct {
	service *service.SecurityAuditService
}

func (r outboxAwareSecurityAuditRecorder) Record(ctx context.Context, in service.SecurityAuditInput) (store.SecurityAuditEvent, error) {
	if in.Outcome == "success" && store.SecurityAuditOutboxEnqueued(ctx) {
		return store.SecurityAuditEvent{}, nil
	}
	return r.service.Record(ctx, in)
}

// SecurityAuditWithOutbox は既存監査middlewareへ、同一transaction用の署名済みoutboxイベント作成関数を注入する。
func SecurityAuditWithOutbox(auditService *service.SecurityAuditService, trustedProxyCIDRs []netip.Prefix) func(http.Handler) http.Handler {
	if auditService == nil {
		return SecurityAudit(nil, trustedProxyCIDRs)
	}
	directAudit := SecurityAudit(outboxAwareSecurityAuditRecorder{service: auditService}, trustedProxyCIDRs)
	return func(next http.Handler) http.Handler {
		auditedHandler := directAudit(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			state := &store.SecurityAuditOutboxState{}
			state.Prepare = func(event store.SecurityAuditOutboxEvent) (store.CreateSecurityAuditEventParams, error) {
				routePattern := safeSecurityAuditRoutePattern(r)
				spec, _ := classifySecurityAuditRoute(r.Method, routePattern)
				actorType, actorUserID := resolveOutboxAuditActor(r, spec.ActorType, event.ActorType, event.ActorUserID)
				details := make(map[string]string, len(event.Details)+1)
				for key, value := range event.Details {
					details[key] = value
				}
				details["schema_version"] = "1"
				outcome := strings.TrimSpace(event.Outcome)
				if outcome == "" {
					outcome = "success"
				}
				severity := strings.TrimSpace(event.Severity)
				if severity == "" {
					severity = spec.Severity
				}
				resourceType := strings.TrimSpace(event.ResourceType)
				if resourceType == "" {
					resourceType = spec.ResourceType
				}
				return auditService.Prepare(service.SecurityAuditInput{
					EventType:      event.EventType,
					Severity:       severity,
					Outcome:        outcome,
					ActorType:      actorType,
					ActorUserID:    actorUserID,
					OrganizationID: event.OrganizationID,
					ResourceType:   resourceType,
					ResourceID:     event.ResourceID,
					RequestID:      chimw.GetReqID(r.Context()),
					SourceService:  "api",
					HTTPMethod:     r.Method,
					RoutePattern:   routePattern,
					StatusCode:     event.StatusCode,
					ClientIP:       ClientIP(r, trustedProxyCIDRs),
					UserAgent:      r.UserAgent(),
					Details:        details,
				})
			}
			ctx := store.WithSecurityAuditOutboxState(r.Context(), state)
			auditedHandler.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func resolveOutboxAuditActor(r *http.Request, routeActorType, eventActorType string, eventActorUserID *uuid.UUID) (string, *uuid.UUID) {
	actorType := strings.TrimSpace(eventActorType)
	if actorType == "" {
		actorType = strings.TrimSpace(routeActorType)
	}
	var actorUserID *uuid.UUID
	if user, authenticated := AuthUserFromContext(r.Context()); authenticated {
		actorType = "user"
		id := user.ID
		actorUserID = &id
	}
	if eventActorUserID != nil && *eventActorUserID != uuid.Nil {
		actorType = "user"
		id := *eventActorUserID
		actorUserID = &id
	}
	if actorType == "" {
		actorType = "anonymous"
	}
	if actorType == "user" && actorUserID == nil {
		actorType = "anonymous"
	}
	return actorType, actorUserID
}
