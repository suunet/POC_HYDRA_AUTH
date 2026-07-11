ARG GO_VERSION=1.25

FROM golang:${GO_VERSION}-bookworm AS builder
# ビルド対象のエントリポイント（app=app-service / auth=auth-service）
ARG CMD=app
WORKDIR /src

COPY go.work* ./
COPY backend ./backend

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /bin/server ./backend/cmd/${CMD}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /bin/server /server
USER nonroot
ENTRYPOINT ["/server"]
