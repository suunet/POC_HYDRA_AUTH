package unit

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"poc-app-hydra/backend"
	applog "poc-app-hydra/backend/common/log"
	"poc-app-hydra/backend/healthcheck/app/query"
)

// UC-001: 主成功シナリオ — クエリハンドラが status ok を返す
func TestUC001_HealthQuery_ReturnsOK(t *testing.T) {
	res := query.NewHealthHandler().Handle(context.Background())
	assert.Equal(t, "ok", res.Status)
}

// UC-001: UseCase開始/終了のINFOログ（NFR-08）
func TestUC001_HealthQuery_EmitsUseCaseLogs(t *testing.T) {
	var buf bytes.Buffer
	ctx := applog.ContextWithLogger(context.Background(), applog.New(&buf, "app-service"))

	query.NewHealthHandler().Handle(ctx)

	logs := strings.TrimSpace(buf.String())
	lines := strings.Split(logs, "\n")
	require.Len(t, lines, 2, "usecase must emit start and finish logs (NFR-08)")
	for _, line := range lines {
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec))
		assert.Equal(t, "INFO", rec["lvl"], "usecase logs must be INFO (NFR-08)")
		assert.Equal(t, "healthcheck", rec["ctx"])
	}
}

func newTestEcho(t *testing.T) http.Handler {
	t.Helper()
	e, err := backend.Build(context.Background(), applog.New(&bytes.Buffer{}, "app-service"))
	require.NoError(t, err)
	return e
}

// UC-001: 主成功シナリオ — GET /health が 200/JSON を返す
func TestUC001_HealthCheck_ReturnsOK(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestEcho(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
}

// UC-001: 代替フロー — 未定義パスは404（Q-9）
func TestUC001_UnknownPath_Returns404(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestEcho(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/unknown", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// UC-001: 代替フロー — 非対応メソッドは405（Q-9）
func TestUC001_HealthMethodNotAllowed_Returns405(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestEcho(t).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/health", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
