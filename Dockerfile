# Build the Go service and its pinned Python SQL translation runtime.
FROM golang:1.26.1 AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/main ./cmd/server \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/replica-ingress ./cmd/replica-ingress \
    && go build -trimpath -o /app/license-bundle-generator ./cmd/license-bundle \
    && /app/license-bundle-generator --output /app/go-third-party-licenses

FROM python:3.12-alpine3.22 AS sql-runtime
COPY requirements-sql-translation.txt /tmp/requirements-sql-translation.txt
RUN python -m pip install --no-cache-dir --target=/opt/data-fabric/python --requirement /tmp/requirements-sql-translation.txt

FROM alpine:3.22
ARG DATA_FABRIC_SOURCE_REVISION=unknown
LABEL org.opencontainers.image.title="Data Fabric" \
      org.opencontainers.image.revision="${DATA_FABRIC_SOURCE_REVISION}"
RUN apk --no-cache add ca-certificates tzdata python3 \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app appuser

WORKDIR /app
ENV SQL_TRANSLATION_PYTHON_PATH=/usr/bin/python3 \
    SQL_TRANSLATION_SCRIPT_PATH=/app/scripts/sql_translate.py \
    SQL_TRANSLATION_PYTHON_MODULE_PATH=/opt/data-fabric/python

COPY --from=builder --chown=10001:10001 /app/main /app/main
COPY --from=builder --chown=10001:10001 /app/replica-ingress /app/replica-ingress
COPY --from=builder --chown=10001:10001 /app/configs/config.yaml.example /app/configs/config.yaml.example
COPY --from=builder --chown=10001:10001 /app/LICENSE /app/LICENSE
COPY --from=builder --chown=10001:10001 /app/THIRD_PARTY_NOTICES.md /app/THIRD_PARTY_NOTICES.md
COPY --from=builder --chown=10001:10001 /app/go-third-party-licenses /app/third-party-licenses/go
COPY --from=builder --chown=10001:10001 /app/requirements-sql-translation.txt /app/requirements-sql-translation.txt
COPY --from=builder --chown=10001:10001 /app/scripts/sql_translate.py /app/scripts/sql_translate.py
COPY --from=sql-runtime --chown=10001:10001 /opt/data-fabric/python /opt/data-fabric/python

USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/main"]
