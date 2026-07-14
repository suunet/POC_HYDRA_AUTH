package http

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"

	commonhttp "poc-app-hydra/backend/common/http"
)

type Handler struct{}

func NewHandler() *Handler {
	return &Handler{}
}

func (h *Handler) RegisterAccount(ctx context.Context, req RegisterAccountRequestObject) (RegisterAccountResponseObject, error) {
	// TODO: T-005 サイクル4-5で UC-002（アカウントを登録する）を実装する
	return nil, commonhttp.NewProblemError(http.StatusNotImplemented, "not-implemented", "UC-002 は実装中")
}

func Register(e *echo.Echo, h *Handler) {
	RegisterHandlers(e, NewStrictHandler(h, nil))
}
