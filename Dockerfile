# Build stage: keep this Go version aligned with the go directive in go.mod.
FROM golang:1.26.1 AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /app/main ./cmd/server

# Runtime stage
FROM alpine:3.22
RUN apk --no-cache add ca-certificates tzdata \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app appuser

WORKDIR /app
COPY --from=builder --chown=10001:10001 /app/main /app/main
COPY --from=builder --chown=10001:10001 /app/configs/config.yaml.example /app/configs/config.yaml.example

USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/main"]
