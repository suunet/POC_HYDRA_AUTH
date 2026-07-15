package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"poc-app-hydra/backend/auth/app/command"
	"poc-app-hydra/backend/auth/domain"
	commonhttp "poc-app-hydra/backend/common/http"
)

type Handler struct {
	register *command.RegisterAccountHandler
}

func NewHandler(register *command.RegisterAccountHandler) *Handler {
	return &Handler{register: register}
}

func (h *Handler) RegisterAccount(ctx context.Context, req RegisterAccountRequestObject) (RegisterAccountResponseObject, error) {
	if req.Body == nil {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "リクエストボディが必要です")
	}

	err := h.register.Handle(ctx, string(req.Body.Email), req.Body.Password)
	switch {
	case err == nil:
		return RegisterAccount201Response{}, nil
	case errors.Is(err, domain.ErrInvalidEmail):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "メールアドレスの形式が不正です")
	case errors.Is(err, domain.ErrInvalidPassword):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "パスワードは15〜64文字で指定してください")
	case errors.Is(err, command.ErrRateLimited):
		problem := commonhttp.NewProblemError(http.StatusTooManyRequests, "rate-limit-exceeded", "登録リクエストが多すぎます")
		return nil, problem.WithRetryAfter(int(domain.RegistrationRateLimitWindow.Seconds()))
	case errors.Is(err, command.ErrMailDeliveryFail):
		return nil, commonhttp.NewProblemError(http.StatusServiceUnavailable, "mail-delivery-error", "確認メールの送信に失敗しました")
	default:
		return nil, err
	}
}

func Register(e *echo.Echo, h *Handler) {
	RegisterHandlers(e, NewStrictHandler(h, nil))
}
