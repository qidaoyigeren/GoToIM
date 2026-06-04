# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.25
ARG ALPINE_VERSION=3.22

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

ARG SERVICE

ENV CGO_ENABLED=0 \
    GO111MODULE=on \
    GOPROXY=https://goproxy.cn,direct

WORKDIR /src

RUN apk add --no-cache ca-certificates git tzdata

COPY go.mod go.sum ./
COPY third_party/ ./third_party/
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    test -n "${SERVICE}" && \
    test -f "./cmd/${SERVICE}/main.go" && \
    go build -trimpath -ldflags="-s -w" -o /out/server "./cmd/${SERVICE}"

FROM alpine:${ALPINE_VERSION} AS runtime

RUN apk add --no-cache ca-certificates netcat-openbsd tzdata && \
    addgroup -S goim && \
    adduser -S -D -H -h /nonexistent -s /sbin/nologin -G goim goim

WORKDIR /

COPY --from=builder /out/server /server

USER goim:goim

EXPOSE 3101 3102 3109 3111 3119

ENTRYPOINT ["/server"]
