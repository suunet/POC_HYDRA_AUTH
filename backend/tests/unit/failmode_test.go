package unit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	applog "poc-app-hydra/backend/common/log"
	"poc-app-hydra/backend/common/ratelimit"
)

type fakeLimiter struct {
	result ratelimit.Result
	err    error
	calls  int
}

func (f *fakeLimiter) Allow(ctx context.Context, key string) (ratelimit.Result, error) {
	f.calls++
	return f.result, f.err
}

func ctxWithLogBuffer() (context.Context, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := applog.New(&buf, "auth-service")
	return applog.ContextWithLogger(context.Background(), logger), &buf
}

// UC-002 / NFR-08: fail-open は内側の判定失敗（外部依存失敗）時に通過させ、ERRORログを出す。
func TestFailModeLimiterFailOpenAllowsAndLogsError(t *testing.T) {
	ctx, buf := ctxWithLogBuffer()
	inner := &fakeLimiter{err: errors.New("redis down")}
	limiter := ratelimit.NewFailModeLimiter(inner, ratelimit.FailOpen)

	res, err := limiter.Allow(ctx, "user@example.com")

	require.NoError(t, err, "fail-open は内側エラーを飲み込む")
	assert.True(t, res.Allowed, "fail-open は障害時に通過させる")

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec), "ERRORログがJSONで出る")
	assert.Equal(t, "ERROR", rec["lvl"], "NFR-08: 外部依存失敗はERROR")
	msg, _ := rec["msg"].(string)
	assert.Contains(t, msg, "fail-open", "どのfail-modeで発火したか判別できる")
	assert.Contains(t, rec, "error", "内側エラーをerrorフィールドに記録")
	assert.NotContains(t, buf.String(), "user@example.com", "NFR-09: キー（メール）をログに書かない")
}

// UC-002 / NFR-08: fail-closed は内側の判定失敗時に拒否し、ERRORログを出す。
func TestFailModeLimiterFailClosedDeniesAndLogsError(t *testing.T) {
	ctx, buf := ctxWithLogBuffer()
	inner := &fakeLimiter{err: errors.New("redis down")}
	limiter := ratelimit.NewFailModeLimiter(inner, ratelimit.FailClosed)

	res, err := limiter.Allow(ctx, "user@example.com")

	require.NoError(t, err)
	assert.False(t, res.Allowed, "fail-closed は障害時に拒否する")

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	assert.Equal(t, "ERROR", rec["lvl"])
	msg, _ := rec["msg"].(string)
	assert.Contains(t, msg, "fail-closed", "どのfail-modeで発火したか判別できる")
	assert.Contains(t, rec, "error", "内側エラーをerrorフィールドに記録")
	assert.NotContains(t, buf.String(), "user@example.com", "NFR-09: キー（メール）をログに書かない")
}

// UC-002: 内側が正常判定を返す場合はそのまま素通しし、ログを出さない。
func TestFailModeLimiterPassesThroughWhenInnerSucceeds(t *testing.T) {
	ctx, buf := ctxWithLogBuffer()
	inner := &fakeLimiter{result: ratelimit.NewResult(true, 0, 0, time.Time{})}
	limiter := ratelimit.NewFailModeLimiter(inner, ratelimit.FailClosed)

	res, err := limiter.Allow(ctx, "user@example.com")

	require.NoError(t, err)
	assert.True(t, res.Allowed, "内側の判定をそのまま返す")
	assert.Empty(t, buf.String(), "正常時はログを出さない")
}
