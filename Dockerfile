# syntax=docker/dockerfile:1
#
# Network downloads first use the build's HTTP(S)_PROXY (if any) and fall back
# to a direct connection, so the image builds both behind a proxy and without.

# ---- Frontend build ----
FROM node:22-slim AS web
WORKDIR /web
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund \
    || env -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# ---- Backend build ----
FROM golang:1.26-alpine AS api
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download \
    || env -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ---- Runtime ----
FROM alpine:3.22
# Chromium renders every PDF (same engine as the official letterhead); fonts are
# installed by the application into a private fontconfig at runtime.
RUN (apk add --no-cache ca-certificates tzdata chromium \
     || env -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy apk add --no-cache ca-certificates tzdata chromium) \
    && adduser -D -H -u 10001 app \
    && mkdir -p /data/attachments \
    && chown -R app:app /data
COPY --from=api /out/server /app/server
COPY --from=web /web/dist /app/web
ENV STATIC_DIR=/app/web \
    HOME=/tmp \
    STORAGE_DIR=/data/attachments \
    APP_ENV=production
USER app
VOLUME ["/data/attachments"]
EXPOSE 8085
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s CMD wget -qO- "http://127.0.0.1:${APP_PORT:-8085}/healthz" >/dev/null || exit 1
ENTRYPOINT ["/app/server"]
