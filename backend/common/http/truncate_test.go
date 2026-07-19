package http

// NOTE: 非公開関数の検証のため同居テスト（テスト集約正典の例外・structure.md §4に記録）。
// 参照元 truncate_test.go の受入基準の圧縮移植。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTruncateBodyForLog(t *testing.T) {
	t.Run("512バイト以下はそのまま", func(t *testing.T) {
		body := strings.Repeat("a", 512)
		assert.Equal(t, body, truncateBodyForLog(body))
	})

	t.Run("非JSONの超過は先頭末尾を残しバイト数を記す", func(t *testing.T) {
		got := truncateBodyForLog(strings.Repeat("a", 600))
		assert.Contains(t, got, "(88 bytes truncated)")
		assert.Less(t, len(got), 600)
	})

	t.Run("JSON配列は先頭5要素とマーカーと末尾1要素に刈られ有効JSONを保つ", func(t *testing.T) {
		var items []int
		for i := range 300 {
			items = append(items, i)
		}
		raw, _ := json.Marshal(map[string]any{"items": items})

		got := truncateBodyForLog(string(raw))

		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(got), &parsed), "切り詰め後も有効JSON")
		arr := parsed["items"].([]any)
		assert.Len(t, arr, maxArrayLogItems+1)
		assert.Contains(t, got, "items truncated")
		assert.EqualValues(t, 299, arr[len(arr)-1], "末尾要素を保持")
	})

	t.Run("空文字は空のまま", func(t *testing.T) {
		assert.Empty(t, truncateBodyForLog(""))
	})
}

// NFR-09: 機密キー（password/token等）はネストも含め[REDACTED]化し、平文をログに残さない
func TestRedactBodyForLog(t *testing.T) {
	got := redactBodyForLog(`{"email":"a@example.com","password":"secret-passw0rd!","nested":{"token":"tok123"}}`)

	assert.NotContains(t, got, "secret-passw0rd!")
	assert.NotContains(t, got, "tok123")
	assert.Contains(t, got, "[REDACTED]")
	assert.Contains(t, got, "a@example.com", "機密でないキーは保持")
	assert.Equal(t, "<non-json body (28 bytes)>", redactBodyForLog("password=FormLeakCheck456!xx"), "非JSONは原文を出さない")
	assert.NotContains(t, redactBodyForLog(`{"current_password":"old!","new_password":"new!"}`), "old!", "部分一致キーも伏字")
	assert.Empty(t, redactBodyForLog(""))
	scalar := redactBodyForLog(`"password=StringLeak999!"`)
	assert.NotContains(t, scalar, "StringLeak999!", "JSON文字列スカラーも原文を出さない")
	assert.Contains(t, scalar, "<non-json body")
}
