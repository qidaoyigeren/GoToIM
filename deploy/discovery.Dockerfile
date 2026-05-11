FROM golang:latest AS builder
ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /app
COPY deploy/discovery/ .
RUN CGO_ENABLED=0 go build -o /app/discovery .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/discovery /discovery
EXPOSE 7171
ENTRYPOINT ["/discovery"]
