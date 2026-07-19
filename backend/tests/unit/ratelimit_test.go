package unit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"poc-app-hydra/backend/common/ratelimit"
)

// UC-002 / VAR-16: レート制限基盤（T-007）。登録の平文メールキーは素通しで扱う（INF-14と対で、登録メールキーは平文運用）。
func TestRateLimitPassthroughHasherReturnsKeyUnchanged(t *testing.T) {
	var hasher ratelimit.KeyHasher = ratelimit.PassthroughHasher{}

	got := hasher.Hash("user@example.com")

	assert.Equal(t, "user@example.com", got)
}

// UC-002 / VAR-16: Result の Remaining は負にならない（下限0）。
func TestRateLimitNewResultClampsNegativeRemaining(t *testing.T) {
	resetAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

	got := ratelimit.NewResult(false, 42*time.Second, -3, resetAt)

	assert.False(t, got.Allowed)
	assert.Equal(t, 42*time.Second, got.RetryAfter)
	assert.Equal(t, 0, got.Remaining, "Remaining は負値を 0 に切り詰める")
	assert.Equal(t, resetAt, got.ResetAt)
}

// UC-002 / VAR-16: Result は与えたフィールドをそのまま保持する。
func TestRateLimitNewResultPreservesFields(t *testing.T) {
	resetAt := time.Date(2026, 7, 18, 12, 5, 0, 0, time.UTC)

	got := ratelimit.NewResult(true, 10*time.Second, 4, resetAt)

	assert.Equal(t, ratelimit.Result{
		Allowed:    true,
		RetryAfter: 10 * time.Second,
		Remaining:  4,
		ResetAt:    resetAt,
	}, got)
}
