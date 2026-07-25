package middleware

import (
	"context"
	"log"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/example/vaultsend/internal/service"
	"github.com/example/vaultsend/internal/store"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

const securityAuditPersistTimeout = 3 * time.Second

type SecurityAuditRecorder interface {
	Record(ctx context.Context, in service.SecurityAuditInput) (store.SecurityAuditEvent, error)
}

type securityAuditContextKey string

const securityAuditAttributesKey securityAuditContextKey = "security_audit_attributes"

type SecurityAuditAttributes struct {
	ActorUserID    *uuid.UUID
	OrganizationID *uuid.UUID
	ResourceType   string
	ResourceID     *uuid.UUID
	Details        map[string]string
}

type securityAuditRoute struct {
	EventType       string
	Severity        string
	ActorType       string
	ResourceType    string
	ResourceIDParam string
	OrganizationID  string
}

func SecurityAudit(recorder SecurityAuditRecorder, trustedProxyCIDRs []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if recorder == nil {
				next.ServeHTTP(w, r)
				return
			}

			attributes := &SecurityAuditAttributes{Details: map[string]string{}}
			ctx := context.WithValue(r.Context(), securityAuditAttributesKey, attributes)
			r = r.WithContext(ctx)
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			routePattern := safeSecurityAuditRoutePattern(r)
			spec, ok := classifySecurityAuditRoute(r.Method, routePattern)
			if !ok {
				return
			}

			statusCode := ww.Status()
			if statusCode == 0 {
				statusCode = http.StatusOK
			}
			outcome := securityAuditOutcome(statusCode)
			severity := spec.Severity
			if statusCode >= http.StatusInternalServerError {
				severity = "critical"
			} else if outcome != "success" {
				severity = "warning"
			}

			actorType := spec.ActorType
			var actorUserID *uuid.UUID
			if user, authenticated := AuthUserFromContext(r.Context()); authenticated {
				actorType = "user"
				id := user.ID
				actorUserID = &id
			}
			if attributes.ActorUserID != nil && *attributes.ActorUserID != uuid.Nil {
				actorType = "user"
				id := *attributes.ActorUserID
				actorUserID = &id
			}

			organizationID := attributes.OrganizationID
			if organizationID == nil && spec.OrganizationID != "" {
				organizationID = parsedUUIDParam(r, spec.OrganizationID)
			}
			resourceType := spec.ResourceType
			if strings.TrimSpace(attributes.ResourceType) != "" {
				resourceType = attributes.ResourceType
			}
			resourceID := attributes.ResourceID
			if resourceID == nil && spec.ResourceIDParam != "" {
				resourceID = parsedUUIDParam(r, spec.ResourceIDParam)
			}

			details := make(map[string]string, len(attributes.Details)+1)
			for key, value := range attributes.Details {
				details[key] = value
			}
			details["schema_version"] = "1"

			auditCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), securityAuditPersistTimeout)
			defer cancel()
			_, err := recorder.Record(auditCtx, service.SecurityAuditInput{
				EventType:      spec.EventType,
				Severity:       severity,
				Outcome:        outcome,
				ActorType:      actorType,
				ActorUserID:    actorUserID,
				OrganizationID: organizationID,
				ResourceType:   resourceType,
				ResourceID:     resourceID,
				RequestID:      chimw.GetReqID(r.Context()),
				SourceService:  "api",
				HTTPMethod:     r.Method,
				RoutePattern:   routePattern,
				StatusCode:     statusCode,
				ClientIP:       ClientIP(r, trustedProxyCIDRs),
				UserAgent:      r.UserAgent(),
				Details:        details,
			})
			if err != nil {
				log.Printf(
					"event=security_audit_persist_failed audit_event_type=%s request_id=%s error=%q",
					spec.EventType,
					chimw.GetReqID(r.Context()),
					err.Error(),
				)
			}
		})
	}
}

func SetSecurityAuditActorUserID(ctx context.Context, userID uuid.UUID) {
	if attributes := securityAuditAttributesFromContext(ctx); attributes != nil && userID != uuid.Nil {
		id := userID
		attributes.ActorUserID = &id
	}
}

func SetSecurityAuditOrganizationID(ctx context.Context, organizationID uuid.UUID) {
	if attributes := securityAuditAttributesFromContext(ctx); attributes != nil && organizationID != uuid.Nil {
		id := organizationID
		attributes.OrganizationID = &id
	}
}

func SetSecurityAuditResource(ctx context.Context, resourceType string, resourceID uuid.UUID) {
	if attributes := securityAuditAttributesFromContext(ctx); attributes != nil {
		attributes.ResourceType = strings.TrimSpace(strings.ToLower(resourceType))
		if resourceID != uuid.Nil {
			id := resourceID
			attributes.ResourceID = &id
		}
	}
}

func SetSecurityAuditDetail(ctx context.Context, key, value string) {
	attributes := securityAuditAttributesFromContext(ctx)
	if attributes == nil {
		return
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return
	}
	if attributes.Details == nil {
		attributes.Details = map[string]string{}
	}
	attributes.Details[key] = value
}

func securityAuditAttributesFromContext(ctx context.Context) *SecurityAuditAttributes {
	attributes, _ := ctx.Value(securityAuditAttributesKey).(*SecurityAuditAttributes)
	return attributes
}

func safeSecurityAuditRoutePattern(r *http.Request) string {
	if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
		if pattern := strings.TrimSpace(routeContext.RoutePattern()); pattern != "" {
			return pattern
		}
	}
	normalized := normalizedRateLimitEndpoint(r.Method, r.URL.Path)
	return strings.TrimSpace(strings.TrimPrefix(normalized, r.Method+" "))
}

func classifySecurityAuditRoute(method, routePattern string) (securityAuditRoute, bool) {
	key := strings.ToUpper(strings.TrimSpace(method)) + " " + strings.TrimSpace(routePattern)
	routes := map[string]securityAuditRoute{
		"POST /v1/auth/register":                  {EventType: "auth.register", Severity: "info", ActorType: "anonymous", ResourceType: "user"},
		"POST /v1/auth/login":                     {EventType: "auth.login", Severity: "info", ActorType: "anonymous", ResourceType: "session"},
		"POST /v1/auth/logout":                    {EventType: "auth.logout", Severity: "info", ActorType: "user", ResourceType: "session"},
		"POST /v1/uploads":                        {EventType: "upload.create", Severity: "info", ActorType: "anonymous", ResourceType: "upload"},
		"POST /v1/uploads/{id}/complete":          {EventType: "upload.complete", Severity: "info", ActorType: "anonymous", ResourceType: "upload", ResourceIDParam: "id"},
		"POST /v1/shipments":                      {EventType: "shipment.create", Severity: "info", ActorType: "anonymous", ResourceType: "shipment"},
		"POST /v1/shipments/{id}/resend":          {EventType: "shipment.resend", Severity: "warning", ActorType: "user", ResourceType: "shipment", ResourceIDParam: "id"},
		"DELETE /v1/shipments/{id}":               {EventType: "shipment.delete", Severity: "warning", ActorType: "user", ResourceType: "shipment", ResourceIDParam: "id"},
		"POST /v1/access/{token}/verify":          {EventType: "access.verify", Severity: "info", ActorType: "recipient", ResourceType: "access_token"},
		"GET /v1/files/{id}/download-url":         {EventType: "file.download_url.issue", Severity: "info", ActorType: "recipient", ResourceType: "file", ResourceIDParam: "id"},
		"POST /v1/orgs":                           {EventType: "organization.create", Severity: "info", ActorType: "user", ResourceType: "organization"},
		"POST /v1/orgs/{id}/members":              {EventType: "organization.member.add", Severity: "warning", ActorType: "user", ResourceType: "user", OrganizationID: "id"},
		"DELETE /v1/orgs/{id}/members/{user_id}":  {EventType: "organization.member.remove", Severity: "warning", ActorType: "user", ResourceType: "user", ResourceIDParam: "user_id", OrganizationID: "id"},
		"GET /v1/orgs/{id}/security-audit-events": {EventType: "security_audit.read", Severity: "warning", ActorType: "user", ResourceType: "security_audit", OrganizationID: "id"},
		"POST /v1/billing/checkout":               {EventType: "billing.checkout", Severity: "info", ActorType: "user", ResourceType: "subscription"},
		"POST /v1/billing/webhook":                {EventType: "billing.webhook", Severity: "info", ActorType: "webhook", ResourceType: "subscription"},
	}
	spec, ok := routes[key]
	return spec, ok
}

func securityAuditOutcome(statusCode int) string {
	if statusCode >= 200 && statusCode < 400 {
		return "success"
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return "denied"
	}
	return "failure"
}

func parsedUUIDParam(r *http.Request, name string) *uuid.UUID {
	value := strings.TrimSpace(chi.URLParam(r, name))
	parsed, err := uuid.Parse(value)
	if err != nil || parsed == uuid.Nil {
		return nil
	}
	return &parsed
}
