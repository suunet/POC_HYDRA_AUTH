package http

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	applog "poc-app-hydra/backend/common/log"
)

const ContentTypeProblemJSON = "application/problem+json"

// NOTE: VAR-15（RFC 9457 type URIベースドメイン）。
const ProblemTypeBase = "https://example.com/probs/"

type Problem struct {
	Type             string `json:"type"`
	Title            string `json:"title"`
	Status           int    `json:"status"`
	Detail           string `json:"detail,omitempty"`
	Instance         string `json:"instance,omitempty"`
	RetryAfter       *int   `json:"retry_after,omitempty"` // VAR-16/E4（レート制限。単位はQ-12で確定）
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

func ProblemErrorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}

	problem := Problem{
		Type:   "about:blank",
		Title:  http.StatusText(http.StatusInternalServerError),
		Status: http.StatusInternalServerError,
	}

	var pe *ProblemError
	var he *echo.HTTPError
	switch {
	case errors.As(err, &pe):
		problem = pe.Problem
	case errors.As(err, &he):
		problem.Status = he.Code
		problem.Title = http.StatusText(he.Code)
		if msg, ok := he.Message.(string); ok && msg != problem.Title {
			problem.Detail = msg
		}
		// NOTE: リクエスト解釈段階（バインド・生成コードの形式検証）の400もE1/E2と同じ validation-error として整形する（NFR-06・独自判断はstructure.md §4）
		if he.Code == http.StatusBadRequest {
			problem.Type = ProblemTypeBase + "validation-error"
			// NFR-08: ビジネス例外（入力不正）はWARNING
			applog.FromContext(c.Request().Context()).WarnContext(c.Request().Context(), "リクエスト解釈エラー", "ctx", "http")
		}
	default:
		applog.FromContext(c.Request().Context()).With("ctx", "http", "error", err).Error("handling http error")
	}

	problem.Instance = c.Request().RequestURI

	c.Response().Header().Set(echo.HeaderContentType, ContentTypeProblemJSON)
	if writeErr := c.JSON(problem.Status, problem); writeErr != nil {
		applog.FromContext(c.Request().Context()).With("ctx", "http", "error", writeErr).Error("failed to send problem response")
	}
}
