package handler

import (
	"errors"
	"net/http"

	"github.com/example/vaultsend/internal/http/render"
	"github.com/example/vaultsend/internal/service"
	"github.com/example/vaultsend/internal/store"
	chimw "github.com/go-chi/chi/v5/middleware"
)

func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *service.APIError
	if errors.As(err, &apiErr) {
		render.ServiceError(w, apiErr, chimw.GetReqID(r.Context()))
		return
	}
	if errors.Is(err, store.ErrOrganizationSeatLimit) {
		render.ServiceError(w, &service.APIError{
			Status:          http.StatusForbidden,
			ErrorType:       service.PlanLimitErrorType,
			Code:            "SEAT_LIMIT",
			Message:         "招待を含むメンバー数がseat上限に達しています",
			UpgradeRequired: true,
			UpgradeURL:      "/settings/billing",
			RecommendedPlan: service.RecommendedPlanPro,
		}, chimw.GetReqID(r.Context()))
		return
	}
	render.Error(w, http.StatusInternalServerError, "internal_error", "内部エラーが発生しました", chimw.GetReqID(r.Context()))
}
