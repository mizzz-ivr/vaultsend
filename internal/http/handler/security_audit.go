package handler

import (
	"fmt"
	"net/http"

	"github.com/example/vaultsend/internal/http/middleware"
	"github.com/example/vaultsend/internal/http/render"
	"github.com/example/vaultsend/internal/service"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

type SecurityAuditHandler struct {
	Service *service.SecurityAuditService
}

func (h SecurityAuditHandler) ListOrganizationEvents(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.AuthUserFromContext(r.Context())
	if !ok {
		render.Error(w, http.StatusUnauthorized, "unauthorized", "ログインが必要です", chimw.GetReqID(r.Context()))
		return
	}
	organizationID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_org_id", "organization id が不正です", chimw.GetReqID(r.Context()))
		return
	}
	limit, offset, ok := parseLimitOffset(w, r)
	if !ok {
		return
	}
	out, err := h.Service.ListOrganizationEvents(r.Context(), user.ID, organizationID, limit, offset)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	middleware.SetSecurityAuditOrganizationID(r.Context(), organizationID)
	middleware.SetSecurityAuditDetail(r.Context(), "returned_items", fmt.Sprintf("%d", len(out.Items)))
	render.JSON(w, http.StatusOK, out)
}
