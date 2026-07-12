package unit

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	applog "poc-app-hydra/backend/common/log"
)

// NFR-09: 全ログに ts/lvl/svc/ctx/trace_id/span_id/req_id/msg を含める
func TestNFR09_Logger_EmitsRequiredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := applog.New(&buf, "app-service")

	ctx := applog.ContextWithLogger(context.Background(), logger)
	ctx = applog.ContextWithRequestID(ctx, "req-123")
	ctx = applog.ContextWithTrace(ctx, applog.NewTraceID(), applog.NewSpanID())

	applog.FromContext(ctx).InfoContext(ctx, "hello", "ctx", "healthcheck")

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec), "log output must be JSON")

	for _, key := range []string{"ts", "lvl", "svc", "ctx", "trace_id", "span_id", "req_id", "msg"} {
		t.Run(key, func(t *testing.T) {
			assert.Contains(t, rec, key, "NFR-09 required field")
		})
	}
	assert.Equal(t, "app-service", rec["svc"])
	assert.Equal(t, "hello", rec["msg"])
	assert.Equal(t, "req-123", rec["req_id"])
}

// NFR-09: req_id が無い場合は生成し、生成品と区別できる gen_ プレフィックスを付ける
func TestNFR09_RequestID_GeneratedWithPrefix(t *testing.T) {
	id := applog.RequestIDFromContext(context.Background())
	assert.True(t, len(id) > 4 && id[:4] == "gen_", "generated req_id must have gen_ prefix, got %q", id)
}

// Q-6: OTel互換のID生成のみの薄い実装（trace=32hex, span=16hex）
func TestNFR09_TraceAndSpanID_OTelCompatibleFormat(t *testing.T) {
	assert.Len(t, applog.NewTraceID(), 32, "trace_id must be OTel-compatible 32 hex chars")
	assert.Len(t, applog.NewSpanID(), 16, "span_id must be OTel-compatible 16 hex chars")
}

func TestNFR09_FromContext_FallsBackToDefault(t *testing.T) {
	require.NotNil(t, applog.FromContext(context.Background()))
}
