# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.25
ARG ALPINE_VERSION=3.22

FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

ENV CGO_ENABLED=0 \
    GO111MODULE=on \
    GOPROXY=https://goproxy.cn,direct

WORKDIR /src

RUN apk add --no-cache ca-certificates git tzdata

COPY deploy/discovery/ ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/discovery .

FROM alpine:${ALPINE_VERSION} AS runtime

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S goim && \
    adduser -S -D -H -h /nonexistent -s /sbin/nologin -G goim goim

WORKDIR /

COPY --from=builder /out/discovery /discovery

USER goim:goim

EXPOSE 7171

ENTRYPOINT ["/discovery"]
