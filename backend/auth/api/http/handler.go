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
	verify   *command.VerifyEmailHandler
}

func NewHandler(register *command.RegisterAccountHandler, verify *command.VerifyEmailHandler) *Handler {
	return &Handler{register: register, verify: verify}
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

func (h *Handler) VerifyEmail(ctx context.Context, req VerifyEmailRequestObject) (VerifyEmailResponseObject, error) {
	if req.Body == nil {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "リクエストボディが必要です")
	}

	// NOTE: openapiのminLength:1をバインダは検証しないため必須検査をここで行う（E4より前＝形式不正は評価順対象外）
	if req.Body.Token == "" {
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "validation-error", "トークンが必要です")
	}

	err := h.verify.Handle(ctx, req.Body.Token, commonhttp.ClientIPFromContext(ctx))
	var verifyRateLimited *command.RateLimitedError
	switch {
	case err == nil:
		return VerifyEmail200Response{}, nil
	case errors.Is(err, command.ErrInvalidToken):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "invalid-token", "メール確認トークンが無効です")
	case errors.Is(err, command.ErrTokenExpired):
		return nil, commonhttp.NewProblemError(http.StatusBadRequest, "token-expired", "メール確認トークンの有効期限が切れています")
	case errors.As(err, &verifyRateLimited):
		problem := commonhttp.NewProblemError(http.StatusTooManyRequests, "rate-limit-exceeded", "メール確認リクエストが多すぎます")
		return nil, problem.WithRetryAfter(int(math.Ceil(verifyRateLimited.RetryAfter.Seconds())))
	default:
		return nil, err
	}
}

// TODO: サイクル3のTDDでUC-004（メール確認トークンを再送する）本体を実装する（T-008）
func (h *Handler) ResendEmailVerification(ctx context.Context, req ResendEmailVerificationRequestObject) (ResendEmailVerificationResponseObject, error) {
	return nil, echo.NewHTTPError(http.StatusNotImplemented)
}

func Register(e *echo.Echo, h *Handler) {
	RegisterHandlers(e, NewStrictHandler(h, nil))
}
