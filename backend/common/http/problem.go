package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	applog "poc-app-hydra/backend/common/log"
)

const ContentTypeProblemJSON = "application/problem+json"

// NOTE: RFC 9457 type のベースURI。POCのため仮置き
const ProblemTypeBase = "https://example.com/probs/"

type Problem struct {
	Type             string `json:"type"`
	Title            string `json:"title"`
	Status           int    `json:"status"`
	Detail           string `json:"detail,omitempty"`
	Instance         string `json:"instance,omitempty"`
	RetryAfter       *int   `json:"retry_after,omitempty"` // NOTE: レート制限・ロックアウト時の再試行可能秒数
	ErrorCode        string `json:"error_code,omitempty"`  // NOTE: VAR-11拡張（account_locked等）
	RevocationReason string `json:"revocation_reason,omitempty"`
}

type ProblemError struct {
	Problem Problem
}

func (e *ProblemError) Error() string {
	return e.Problem.Title + ": " + e.Problem.Detail
}

func NewProblemError(status int, typeSlug, detail string) *ProblemError {
	return &ProblemError{Problem: Problem{
		Type:   ProblemTypeBase + typeSlug,
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	}}
}

func (e *ProblemError) WithRetryAfter(seconds int) *ProblemError {
	e.Problem.RetryAfter = &seconds
	return e
}

func (e *ProblemError) WithErrorCode(code string) *ProblemError {
	e.Problem.ErrorCode = code
	return e
}

// StatusFromError はハンドラが返したエラーから最終的なHTTPステータスを求める。
// strict-serverはエラー時に応答を書かず後段の HTTPErrorHandler が status を確定するため、
// ミドルウェア（requestlog）が応答書込前に実 status を知るのに使う（ProblemErrorHandler と同一の決定）。
func StatusFromError(err error) int {
	if err == nil {
		return 0
	}
	var pe *ProblemError
	var he *echo.HTTPError
	switch {
	case errors.As(err, &pe):
		return pe.Problem.Status
	case errors.As(err, &he):
		return he.Code
	default:
		return http.StatusInternalServerError
	}
}

func ProblemErrorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}

	// NOTE: statusの決定は StatusFromError に一本化（requestlogと同一の値になることを構造的に保証・BJ c8#1）。
	// 本switchはTitle/Detail/Type整形とログのみを担う
	status := StatusFromError(err)
	problem := Problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
	}

	var pe *ProblemError
	var he *echo.HTTPError
	switch {
	case errors.As(err, &pe):
		problem = pe.Problem
	case errors.As(err, &he):
		if msg, ok := he.Message.(string); ok && msg != problem.Title {
			problem.Detail = msg
		}
		// NOTE: バインド段階の400もvalidation-errorとして整形する（独自判断: structure.md §4）
		if problem.Status == http.StatusBadRequest {
			problem.Type = ProblemTypeBase + "validation-error"
			applog.FromContext(c.Request().Context()).WarnContext(c.Request().Context(), "リクエスト解釈エラー", "ctx", "http")
		}
	default:
		applog.FromContext(c.Request().Context()).With("ctx", "http", "error", err).Error("handling http error")
	}

	problem.Instance = c.Request().RequestURI

	if problem.RetryAfter != nil {
		c.Response().Header().Set(echo.HeaderRetryAfter, strconv.Itoa(*problem.RetryAfter))
	}
	c.Response().Header().Set(echo.HeaderContentType, ContentTypeProblemJSON)
	if writeErr := c.JSON(problem.Status, problem); writeErr != nil {
		applog.FromContext(c.Request().Context()).With("ctx", "http", "error", writeErr).Error("failed to send problem response")
	}
}
