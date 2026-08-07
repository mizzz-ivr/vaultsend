package http

import (
	stdhttp "net/http"

	"github.com/example/vaultsend/internal/config"
	"github.com/example/vaultsend/internal/http/handler"
	appmw "github.com/example/vaultsend/internal/http/middleware"
	"github.com/example/vaultsend/internal/service"
	"github.com/example/vaultsend/internal/store"
	"github.com/go-chi/chi/v5"
)

func NewServer(cfg config.Config, queries *store.Queries, uploadSvc *service.UploadService, shipmentSvc *service.ShipmentService, accessSvc *service.AccessService, authSvc *service.AuthService, billingSvc *service.BillingService, orgSvc *service.OrgService, auditSvc *service.SecurityAuditService) stdhttp.Handler {
	r := chi.NewRouter()
	rateLimiter := appmw.NewInMemoryRateLimiter()

	r.Use(appmw.RequestID)
	r.Use(appmw.Recovery)
	r.Use(appmw.RequestLogger(cfg.TrustedProxyCIDRs))
	r.Use(appmw.SecurityHeaders(appmw.SecurityHeadersConfig{EnableHSTS: cfg.HSTSEnabled}))
	r.Use(appmw.Timeout(cfg.HTTPRequestTimeout))
	r.Use(appmw.RateLimit(rateLimiter, appmw.RateLimitConfig{
		PerMinuteLimit:    cfg.RateLimitRPS,
		VerifyLimit:       max(10, cfg.VerifyMaxAttempts*2),
		TrustedProxyCIDRs: cfg.TrustedProxyCIDRs,
	}))
	r.Use(appmw.CSRFProtection(appmw.CSRFConfig{AllowedOrigins: cfg.CSRFAllowedOrigins}))
	r.Use(appmw.OptionalAuth(authSvc))
	r.Use(appmw.SecurityAuditWithOutbox(auditSvc, cfg.TrustedProxyCIDRs))
	r.Use(appmw.OptionalPlan(billingSvc))

	uploadHandler := handler.UploadHandler{Service: uploadSvc}
	shipmentHandler := handler.ShipmentHandler{Service: shipmentSvc}
	accessHandler := handler.AccessHandler{
		Service:        accessSvc,
		CookieDomain:   cfg.CookieDomain,
		CookieSecure:   cfg.CookieSecure,
		CookieSameSite: cfg.CookieSameSite,
	}
	authHandler := handler.AuthHandler{
		Service:           authSvc,
		CookieDomain:      cfg.CookieDomain,
		CookieSecure:      cfg.CookieSecure,
		CookieSameSite:    cfg.CookieSameSite,
		TrustedProxyCIDRs: cfg.TrustedProxyCIDRs,
	}
	billingHandler := handler.BillingHandler{Service: billingSvc}
	orgHandler := handler.OrgHandler{Service: orgSvc}
	orgInvitationHandler := handler.OrgInvitationHandler{Service: orgSvc}
	auditHandler := handler.SecurityAuditHandler{Service: auditSvc}
	internalMetricsHandler := handler.InternalMetricsHandler{
		Store:        queries,
		BearerToken:  cfg.InternalMetricsToken,
		QueryTimeout: cfg.InternalMetricsQueryTimeout,
	}

	r.Get("/healthz", handler.Health)
	r.Get("/internal/metrics", internalMetricsHandler.ServeHTTP)
	r.Route("/v1", func(r chi.Router) {
		r.Post("/uploads", uploadHandler.CreateUpload)
		r.Post("/uploads/{id}/complete", uploadHandler.CompleteUpload)
		r.Post("/shipments", shipmentHandler.CreateShipment)
		r.Group(func(r chi.Router) {
			r.Use(appmw.RequireAuth(authSvc))
			r.Get("/shipments", shipmentHandler.ListShipments)
			r.Get("/shipments/{id}", shipmentHandler.GetShipment)
			r.Get("/shipments/{id}/notifications", shipmentHandler.ListShipmentNotifications)
			r.Get("/shipments/{id}/recipients", shipmentHandler.ListShipmentRecipients)
			r.Post("/shipments/{id}/resend", shipmentHandler.ResendShipment)
			r.Delete("/shipments/{id}", shipmentHandler.DeleteShipment)
		})
		r.Get("/access/{token}", accessHandler.InspectAccess)
		r.Post("/access/{token}/verify", accessHandler.VerifyAccess)
		r.Get("/files/{id}/download-url", accessHandler.GenerateDownloadURL)
		r.Get("/invitations/{token}", orgInvitationHandler.Inspect)

		r.Route("/auth", func(r chi.Router) {
			r.Post("/register", authHandler.Register)
			r.Post("/login", authHandler.Login)
			r.Group(func(r chi.Router) {
				r.Use(appmw.RequireAuth(authSvc))
				r.Post("/logout", authHandler.Logout)
				r.Get("/me", authHandler.Me)
			})
		})

		r.Group(func(r chi.Router) {
			r.Use(appmw.RequireAuth(authSvc))
			r.Post("/orgs", orgHandler.CreateOrg)
			r.Get("/orgs", orgHandler.ListOrgs)
			r.Get("/orgs/{id}", orgHandler.GetOrg)
			r.Patch("/orgs/{id}", orgHandler.UpdateOrg)
			r.Post("/orgs/{id}/owner-transfer", orgHandler.TransferOwner)
			r.Post("/orgs/{id}/leave", orgHandler.LeaveOrg)
			r.Post("/orgs/{id}/members", orgHandler.AddMember)
			r.Delete("/orgs/{id}/members/{user_id}", orgHandler.DeleteMember)
			r.Post("/orgs/{id}/invitations", orgInvitationHandler.Create)
			r.Get("/orgs/{id}/invitations", orgInvitationHandler.List)
			r.Post("/orgs/{id}/invitations/{invitation_id}/resend", orgInvitationHandler.Resend)
			r.Delete("/orgs/{id}/invitations/{invitation_id}", orgInvitationHandler.Revoke)
			r.Post("/invitations/{token}/accept", orgInvitationHandler.Accept)
			r.Get("/orgs/{id}/security-audit-events", auditHandler.ListOrganizationEvents)
		})
		r.Group(func(r chi.Router) {
			r.Use(appmw.RequireAuth(authSvc))
			r.Get("/billing/plan", billingHandler.GetPlan)
			r.Post("/billing/checkout", billingHandler.CreateCheckout)
			r.Get("/orgs/{id}/billing", billingHandler.GetOrgBilling)
			r.Get("/orgs/{id}/invoices", billingHandler.ListOrgInvoices)
			r.Get("/orgs/{id}/invoices/{invoice_id}", billingHandler.GetOrgInvoice)
		})
		r.Post("/billing/webhook", billingHandler.Webhook)
	})

	return r
}
