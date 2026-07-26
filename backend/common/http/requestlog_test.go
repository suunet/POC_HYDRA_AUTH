package http

// NOTE: 非公開関数・ミドルウェアの検証のため同居テスト（テスト集約正典の例外・structure.md §4に記録）。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	applog "poc-app-hydra/backend/common/log"
)

// NFR-09: リクエストログのボディからメールアドレス（PII）が伏字される
func TestRedactBodyForLog_RedactsEmail(t *testing.T) {
	out := redactBodyForLog(`{"email":"user@example.com","password":"secret-passw0rd!"}`)
	assert.NotContains(t, out, "user@example.com", "emailは平文で残らない（NFR-09）")
	assert.NotContains(t, out, "secret-passw0rd!", "passwordは従来どおり伏字")
	assert.Contains(t, out, "[REDACTED]")
}

// A（P5後BJ）: エラー応答時のリクエストログ status が実際のHTTPステータスと一致する（200固定でない）
func TestRequestLogMiddleware_ErrorStatusIsReal(t *testing.T) {
	var buf bytes.Buffer
	logger := applog.New(&buf, "test")

	e := echo.New()
	e.HTTPErrorHandler = ProblemErrorHandler
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			ctx := applog.ContextWithLogger(c.Request().Context(), logger)
			c.SetRequest(c.Request().WithContext(ctx))
			return next(c)
		}
	})
	e.Use(requestLogMiddleware)
	e.POST("/x", func(c echo.Context) error {
		return NewProblemError(http.StatusTooManyRequests, "account-locked", "locked")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"email":"user@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusTooManyRequests, rec.Code)

	// 「Request done」ログ行のstatusが429であること
	var found bool
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry["msg"] == "Request done" {
			found = true
			assert.EqualValues(t, http.StatusTooManyRequests, entry["status"], "エラー時もリクエストログのstatusは実値（200固定でない）")
			assert.NotContains(t, entry["request_body"], "user@example.com", "エラー経路でもemailは伏字")
		}
	}
	require.True(t, found, "Request done ログが出力される")
}
