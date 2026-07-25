package handler

import (
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/example/vaultsend/internal/http/middleware"
	"github.com/example/vaultsend/internal/http/render"
	"github.com/example/vaultsend/internal/service"
	chimw "github.com/go-chi/chi/v5/middleware"
)

type AuthHandler struct {
	Service           *service.AuthService
	CookieDomain      string
	CookieSecure      bool
	CookieSameSite    http.SameSite
	TrustedProxyCIDRs []netip.Prefix
}

type authResponse struct {
	User service.AuthUser `json:"user"`
}

type registerRequest struct {
	Email       string  `json:"email"`
	Password    string  `json:"password"`
	DisplayName *string `json:"display_name"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(w, r, &req); err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_request", "不正なJSONです", chimw.GetReqID(r.Context()))
		return
	}
	if strings.TrimSpace(req.Email) == "" || strings.TrimSpace(req.Password) == "" {
		render.Error(w, http.StatusBadRequest, "invalid_request", "email と password は必須です", chimw.GetReqID(r.Context()))
		return
	}
	if req.DisplayName != nil && len([]rune(strings.TrimSpace(*req.DisplayName))) > 80 {
		render.Error(w, http.StatusBadRequest, "invalid_display_name", "display_name が長すぎます", chimw.GetReqID(r.Context()))
		return
	}

	out, err := h.Service.Register(r.Context(), service.RegisterInput{Email: req.Email, Password: req.Password, DisplayName: req.DisplayName})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	h.setSessionCookie(w, out.SessionToken, out.ExpiresAt)
	render.JSON(w, http.StatusCreated, authResponse{User: out.User})
}

func (h AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		render.Error(w, http.StatusBadRequest, "invalid_request", "不正なJSONです", chimw.GetReqID(r.Context()))
		return
	}
	if strings.TrimSpace(req.Email) == "" || strings.TrimSpace(req.Password) == "" {
		render.Error(w, http.StatusBadRequest, "invalid_request", "email と password は必須です", chimw.GetReqID(r.Context()))
		return
	}
	out, err := h.Service.Login(r.Context(), service.LoginInput{
		Email:     req.Email,
		Password:  req.Password,
		UserAgent: readUserAgent(r),
		IPHash:    readIPHash(r, h.TrustedProxyCIDRs),
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	h.setSessionCookie(w, out.SessionToken, out.ExpiresAt)
	render.JSON(w, http.StatusOK, authResponse{User: out.User})
}

func (h AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie(middleware.SessionCookieName)
	if cookie == nil || strings.TrimSpace(cookie.Value) == "" {
		render.Error(w, http.StatusUnauthorized, "unauthorized", "ログインが必要です", chimw.GetReqID(r.Context()))
		return
	}
	if err := h.Service.Logout(r.Context(), cookie.Value); err != nil {
		writeServiceError(w, r, err)
		return
	}
	h.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.AuthUserFromContext(r.Context())
	if !ok {
		render.Error(w, http.StatusUnauthorized, "unauthorized", "ログインが必要です", chimw.GetReqID(r.Context()))
		return
	}
	render.JSON(w, http.StatusOK, authResponse{User: *user})
}

func (h AuthHandler) setSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    token,
		Path:     "/",
		Domain:   h.CookieDomain,
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   h.CookieSecure,
		SameSite: h.cookieSameSite(),
	})
}

func (h AuthHandler) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    "",
		Path:     "/",
		Domain:   h.CookieDomain,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.CookieSecure,
		SameSite: h.cookieSameSite(),
	})
}

func (h AuthHandler) cookieSameSite() http.SameSite {
	if h.CookieSameSite == 0 {
		return http.SameSiteLaxMode
	}
	return h.CookieSameSite
}

func readUserAgent(r *http.Request) *string {
	ua := strings.TrimSpace(r.UserAgent())
	if ua == "" {
		return nil
	}
	return &ua
}

func readIPHash(r *http.Request, trustedProxyCIDRs []netip.Prefix) *string {
	hash := middleware.ClientIPHashFromRequest(r, trustedProxyCIDRs)
	if hash == "" {
		return nil
	}
	return &hash
}
