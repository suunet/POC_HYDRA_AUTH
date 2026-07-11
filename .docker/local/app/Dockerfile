ARG GO_VERSION=1.25
FROM golang:${GO_VERSION}-bookworm

RUN go install github.com/air-verse/air@latest

WORKDIR /workspace

ENV GOMODCACHE=/go/pkg/mod \
    GOCACHE=/root/.cache/go-build

CMD ["sleep", "infinity"]
