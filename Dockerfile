# syntax=docker/dockerfile:1

# ---- build ----
FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/hungarian-law-mcp ./cmd/hungarian-law-mcp

# ---- run ----
FROM alpine:3.22

ENV PORT=3000 \
    HUNGARIAN_LAW_DB_PATH=/data/database.db

RUN apk add --no-cache ca-certificates su-exec \
    && addgroup -S mcp \
    && adduser -S mcp -G mcp \
    && mkdir -p /data /app/dist/bin /app/dist/data \
    && chown mcp:mcp /data

COPY --from=build /out/hungarian-law-mcp /app/dist/bin/hungarian-law-mcp
COPY data/database.db /app/dist/data/database.db
COPY icon.png /app/dist/icon.png
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

# The binary sits two levels under /app/dist so the server's exe-dir/../icon.png
# lookup resolves to /app/dist/icon.png — same layout as the previous image.
RUN chmod +x /usr/local/bin/docker-entrypoint.sh /app/dist/bin/hungarian-law-mcp \
    && chown -R mcp:mcp /app \
    && sha256sum /app/dist/data/database.db | awk '{print $1}' > /app/dist/data/database.db.sha256 \
    && chown mcp:mcp /app/dist/data/database.db.sha256

EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=5 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${PORT:-3000}/health" || exit 1

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/app/dist/bin/hungarian-law-mcp", "serve"]
