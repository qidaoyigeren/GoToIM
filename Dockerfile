FROM golang:latest AS builder
ARG SERVICE
ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /app
COPY go.mod go.sum ./
COPY third_party/ third_party/
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /app/server ./cmd/${SERVICE}/main.go

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/server /server
ENTRYPOINT ["/server"]
