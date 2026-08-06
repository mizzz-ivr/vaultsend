package handler

import (
	"net/http"
	"strings"

	"github.com/example/vaultsend/internal/http/middleware"
	"github.com/example/vaultsend/internal/http/render"
	"github.com/example/vaultsend/internal/service"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

type OrgInvitationHandler struct {
	Service *service.OrgService
}

type createOrgInvitationRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (h OrgInvitationHandler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.AuthUserFromContext(r.Context())
	if !ok {
		render.Error(w, http.StatusUnauthorized, "unauthorized", "ログインが必要です", chimw.GetReqID(r.Context()))
		return
	}
	orgID, ok := parseOrgID(w, r)
	if !ok {
		return
	}
	var req createOrgInvitationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_request", "不正なJSONです", chimw.GetReqID(r.Context()))
		return
	}
	middleware.SetSecurityAuditOrganizationID(r.Context(), orgID)
	middleware.SetSecurityAuditDetail(r.Context(), "role", req.Role)
	out, err := h.Service.CreateInvitation(r.Context(), user.ID, user.Email, orgID, req.Email, req.Role)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	middleware.SetSecurityAuditResource(r.Context(), "organization_invitation", out.ID)
	render.JSON(w, http.StatusCreated, out)
}

func (h OrgInvitationHandler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.AuthUserFromContext(r.Context())
	if !ok {
		render.Error(w, http.StatusUnauthorized, "unauthorized", "ログインが必要です", chimw.GetReqID(r.Context()))
		return
	}
	orgID, ok := parseOrgID(w, r)
	if !ok {
		return
	}
	out, err := h.Service.ListInvitations(r.Context(), user.ID, orgID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	render.JSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h OrgInvitationHandler) Inspect(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(chi.URLParam(r, "token"))
	out, err := h.Service.InspectInvitation(r.Context(), token)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	render.JSON(w, http.StatusOK, out)
}

func (h OrgInvitationHandler) Accept(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.AuthUserFromContext(r.Context())
	if !ok {
		render.Error(w, http.StatusUnauthorized, "unauthorized", "ログインが必要です", chimw.GetReqID(r.Context()))
		return
	}
	token := strings.TrimSpace(chi.URLParam(r, "token"))
	out, err := h.Service.AcceptInvitation(r.Context(), user.ID, user.Email, token)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	middleware.SetSecurityAuditOrganizationID(r.Context(), out.Organization.ID)
	middleware.SetSecurityAuditResource(r.Context(), "organization", out.Organization.ID)
	render.JSON(w, http.StatusOK, out)
}

func (h OrgInvitationHandler) Resend(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.AuthUserFromContext(r.Context())
	if !ok {
		render.Error(w, http.StatusUnauthorized, "unauthorized", "ログインが必要です", chimw.GetReqID(r.Context()))
		return
	}
	orgID, ok := parseOrgID(w, r)
	if !ok {
		return
	}
	invitationID, ok := parseInvitationID(w, r)
	if !ok {
		return
	}
	middleware.SetSecurityAuditOrganizationID(r.Context(), orgID)
	middleware.SetSecurityAuditResource(r.Context(), "organization_invitation", invitationID)
	out, err := h.Service.ResendInvitation(r.Context(), user.ID, user.Email, orgID, invitationID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	render.JSON(w, http.StatusOK, out)
}

func (h OrgInvitationHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.AuthUserFromContext(r.Context())
	if !ok {
		render.Error(w, http.StatusUnauthorized, "unauthorized", "ログインが必要です", chimw.GetReqID(r.Context()))
		return
	}
	orgID, ok := parseOrgID(w, r)
	if !ok {
		return
	}
	invitationID, ok := parseInvitationID(w, r)
	if !ok {
		return
	}
	middleware.SetSecurityAuditOrganizationID(r.Context(), orgID)
	middleware.SetSecurityAuditResource(r.Context(), "organization_invitation", invitationID)
	if err := h.Service.RevokeInvitation(r.Context(), user.ID, orgID, invitationID); err != nil {
		writeServiceError(w, r, err)
		return
	}
	render.JSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func parseOrgID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	orgID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_org_id", "organization id が不正です", chimw.GetReqID(r.Context()))
		return uuid.Nil, false
	}
	return orgID, true
}

func parseInvitationID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	invitationID, err := uuid.Parse(chi.URLParam(r, "invitation_id"))
	if err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_invitation_id", "invitation id が不正です", chimw.GetReqID(r.Context()))
		return uuid.Nil, false
	}
	return invitationID, true
}
