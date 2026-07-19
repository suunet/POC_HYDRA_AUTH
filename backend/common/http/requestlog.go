package http

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	applog "poc-app-hydra/backend/common/log"
)

type bodyCapturingWriter struct {
	http.ResponseWriter
	body *bytes.Buffer
}

func (w *bodyCapturingWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *bodyCapturingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *bodyCapturingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("response writer does not support hijacking")
}

func (w *bodyCapturingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// NOTE: 部分一致（current_password等の将来キーを取りこぼさない・チェッカー指摘#2）
var sensitiveLogKeyParts = []string{"password", "token", "secret"}

func isSensitiveLogKey(key string) bool {
	k := strings.ToLower(key)
	for _, part := range sensitiveLogKeyParts {
		if strings.Contains(k, part) {
			return true
		}
	}
	return false
}

// NOTE: NFR-09（機密情報をログに書かない）のための翻案。参照元は資格情報を扱うAPIを持たず
// 原文のままログするが、authでは平文パスワード・トークンが混入するためJSONの機密キーを伏せる。
func redactBodyForLog(body string) string {
	if body == "" {
		return ""
	}
	var parsed any
	// NOTE: 非JSONはform-encoded等に資格情報が混入しうるため原文を出さない（チェッカー指摘#1・実漏洩の実測あり）
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return fmt.Sprintf("<non-json body (%d bytes)>", len(body))
	}
	switch parsed.(type) {
	case map[string]any, []any:
		out, err := json.Marshal(redactValues(parsed))
		if err != nil {
			return fmt.Sprintf("<non-json body (%d bytes)>", len(body))
		}
		return string(out)
	default:
		// NOTE: 文字列スカラー等は中身を検査できないため原文を出さない（二重エンコードバグ経路の漏洩対策）
		return fmt.Sprintf("<non-json body (%d bytes)>", len(body))
	}
}

func redactValues(v any) any {
	switch val := v.(type) {
	case map[string]any:
		for k, item := range val {
			if isSensitiveLogKey(k) {
				val[k] = "[REDACTED]"
				continue
			}
			val[k] = redactValues(item)
		}
		return val
	case []any:
		for i, item := range val {
			val[i] = redactValues(item)
		}
		return val
	default:
		return v
	}
}

// NOTE: 参照元 requestLogMiddleware の翻案。ボディ捕捉・truncate・Infoレベル1行は同仕様。
// correlation_id 属性は不要（我々の contextHandler が req_id/trace_id を自動付与する＝NFR-09）。
func requestLogMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		req := c.Request()

		var reqBody []byte
		if req.Body != nil {
			reqBody, _ = io.ReadAll(req.Body)
			req.Body = io.NopCloser(bytes.NewBuffer(reqBody))
		}

		respBody := &bytes.Buffer{}
		c.Response().Writer = &bodyCapturingWriter{ResponseWriter: c.Response().Writer, body: respBody}

		start := time.Now()
		err := next(c)
		duration := time.Since(start)

		ctx := req.Context()
		logger := applog.FromContext(ctx).With(
			"ctx", "http",
			"URI", req.RequestURI,
			"method", req.Method,
			"status", c.Response().Status,
			"duration", duration.String(),
			"request_body", truncateBodyForLog(redactBodyForLog(string(reqBody))),
		)
		if err != nil {
			logger = logger.With("error", err.Error())
		}

		resp := respBody.String()
		if !utf8.ValidString(resp) {
			resp = "<binary data>"
		} else if resp = redactBodyForLog(resp); !logger.Enabled(ctx, slog.LevelDebug) {
			resp = truncateBodyForLog(resp)
		}
		logger.InfoContext(ctx, "Request done", "response_body", resp)

		return err
	}
}
