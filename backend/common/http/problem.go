package http

import (
	"errors"
	"net/http"

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
	RetryAfter       *int   `json:"retry_after,omitempty"` // NOTE: レート制限時の再試行可能秒数
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
		// NOTE: バインド段階の400もvalidation-errorとして整形する（独自判断: structure.md §4）
		if he.Code == http.StatusBadRequest {
			problem.Type = ProblemTypeBase + "validation-error"
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
