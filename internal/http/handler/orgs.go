package handler

import (
	"net/http"

	"github.com/example/vaultsend/internal/http/middleware"
	"github.com/example/vaultsend/internal/http/render"
	"github.com/example/vaultsend/internal/service"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

type OrgHandler struct{ Service *service.OrgService }

type createOrgRequest struct {
	Name string `json:"name"`
}
type addMemberRequest struct {
	UserID uuid.UUID `json:"user_id"`
	Role   string    `json:"role"`
}

func (h OrgHandler) CreateOrg(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.AuthUserFromContext(r.Context())
	if !ok {
		render.Error(w, http.StatusUnauthorized, "unauthorized", "ログインが必要です", chimw.GetReqID(r.Context()))
		return
	}
	var req createOrgRequest
	if err := decodeJSON(w, r, &req); err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_request", "不正なJSONです", chimw.GetReqID(r.Context()))
		return
	}
	out, err := h.Service.CreateOrg(r.Context(), user.ID, req.Name)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	middleware.SetSecurityAuditOrganizationID(r.Context(), out.ID)
	middleware.SetSecurityAuditResource(r.Context(), "organization", out.ID)
	render.JSON(w, http.StatusCreated, out)
}

func (h OrgHandler) ListOrgs(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.AuthUserFromContext(r.Context())
	if !ok {
		render.Error(w, http.StatusUnauthorized, "unauthorized", "ログインが必要です", chimw.GetReqID(r.Context()))
		return
	}
	out, err := h.Service.ListUserOrgs(r.Context(), user.ID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	render.JSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h OrgHandler) GetOrg(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.AuthUserFromContext(r.Context())
	if !ok {
		render.Error(w, http.StatusUnauthorized, "unauthorized", "ログインが必要です", chimw.GetReqID(r.Context()))
		return
	}
	orgID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_org_id", "organization id が不正です", chimw.GetReqID(r.Context()))
		return
	}
	org, members, err := h.Service.GetOrg(r.Context(), user.ID, orgID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	render.JSON(w, http.StatusOK, map[string]any{"organization": org, "members": members})
}

func (h OrgHandler) AddMember(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.AuthUserFromContext(r.Context())
	if !ok {
		render.Error(w, http.StatusUnauthorized, "unauthorized", "ログインが必要です", chimw.GetReqID(r.Context()))
		return
	}
	orgID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_org_id", "organization id が不正です", chimw.GetReqID(r.Context()))
		return
	}
	var req addMemberRequest
	if err := decodeJSON(w, r, &req); err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_request", "不正なJSONです", chimw.GetReqID(r.Context()))
		return
	}
	middleware.SetSecurityAuditOrganizationID(r.Context(), orgID)
	middleware.SetSecurityAuditResource(r.Context(), "user", req.UserID)
	middleware.SetSecurityAuditDetail(r.Context(), "role", req.Role)
	out, err := h.Service.AddMember(r.Context(), user.ID, orgID, req.UserID, req.Role)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	render.JSON(w, http.StatusCreated, out)
}

func (h OrgHandler) DeleteMember(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.AuthUserFromContext(r.Context())
	if !ok {
		render.Error(w, http.StatusUnauthorized, "unauthorized", "ログインが必要です", chimw.GetReqID(r.Context()))
		return
	}
	orgID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_org_id", "organization id が不正です", chimw.GetReqID(r.Context()))
		return
	}
	memberID, err := uuid.Parse(chi.URLParam(r, "user_id"))
	if err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_user_id", "user id が不正です", chimw.GetReqID(r.Context()))
		return
	}
	middleware.SetSecurityAuditOrganizationID(r.Context(), orgID)
	middleware.SetSecurityAuditResource(r.Context(), "user", memberID)
	if err := h.Service.RemoveMember(r.Context(), user.ID, orgID, memberID); err != nil {
		writeServiceError(w, r, err)
		return
	}
	render.JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
