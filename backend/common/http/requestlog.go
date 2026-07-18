package http

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
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
			"request_body", truncateBodyForLog(string(reqBody)),
		)
		if err != nil {
			logger = logger.With("error", err.Error())
		}

		resp := respBody.String()
		if !utf8.ValidString(resp) {
			resp = "<binary data>"
		} else if !logger.Enabled(ctx, slog.LevelDebug) {
			resp = truncateBodyForLog(resp)
		}
		logger.InfoContext(ctx, "Request done", "response_body", resp)

		return err
	}
}
