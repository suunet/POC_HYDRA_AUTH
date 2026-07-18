package http

import (
	"encoding/json"
	"fmt"
)

const (
	maxBodyLogBytes  = 512
	maxArrayLogItems = 6 // NOTE: 超過時は先頭5要素＋末尾1要素に切り詰める
)

// NOTE: ログへ出すボディの切り詰め。512バイト以下はそのまま、超過時はJSONなら配列を
// 要素数で刈り、非JSONなら先頭256B＋末尾256Bを残す（参照元 truncate.go と同仕様）。
func truncateBodyForLog(body string) string {
	if body == "" {
		return ""
	}
	if len(body) <= maxBodyLogBytes {
		return body
	}

	var parsed any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return headTailTruncate(body)
	}

	truncated := truncateArrays(parsed)
	out, err := json.Marshal(truncated)
	if err != nil {
		return headTailTruncate(body)
	}
	return string(out)
}

func truncateArrays(v any) any {
	switch val := v.(type) {
	case []any:
		for i, item := range val {
			val[i] = truncateArrays(item)
		}
		if len(val) <= maxArrayLogItems {
			return val
		}
		head := val[:maxArrayLogItems-1]
		marker := map[string]any{"...": fmt.Sprintf("%d items truncated", len(val)-maxArrayLogItems)}
		return append(append(head, marker), val[len(val)-1])
	case map[string]any:
		for k, item := range val {
			val[k] = truncateArrays(item)
		}
		return val
	default:
		return v
	}
}

func headTailTruncate(body string) string {
	const keep = maxBodyLogBytes / 2
	return fmt.Sprintf("%s ... (%d bytes truncated) ... %s", body[:keep], len(body)-2*keep, body[len(body)-keep:])
}
