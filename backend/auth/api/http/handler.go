package http

import (
	"context"
	"errors"
	"math"
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
	var rateLimited *command.RateLimitedError
	switch {
	case err == nil:
		return RegisterAccount201Response{}, nil
	case errors.Is(err, domain.ErrInvalidEmail):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "メールアドレスの形式が不正です")
	case errors.Is(err, domain.ErrInvalidPassword):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "パスワードは15〜64文字で指定してください")
	case errors.As(err, &rateLimited):
		problem := commonhttp.NewProblemError(http.StatusTooManyRequests, "rate-limit-exceeded", "登録リクエストが多すぎます")
		// NOTE: 秒への切り上げで早すぎる再試行を防ぐ（VAR-16）
		return nil, problem.WithRetryAfter(int(math.Ceil(rateLimited.RetryAfter.Seconds())))
	case errors.Is(err, command.ErrMailDeliveryFail):
		return nil, commonhttp.NewProblemError(http.StatusServiceUnavailable, "mail-delivery-error", "確認メールの送信に失敗しました")
	default:
		return nil, err
	}
}

// TODO(T-006): サイクル3のTDDでUC-003（メールアドレスを確認する）本体を実装する
func (h *Handler) VerifyEmail(ctx context.Context, req VerifyEmailRequestObject) (VerifyEmailResponseObject, error) {
	return nil, echo.NewHTTPError(http.StatusNotImplemented)
}

func Register(e *echo.Echo, h *Handler) {
	RegisterHandlers(e, NewStrictHandler(h, nil))
}
