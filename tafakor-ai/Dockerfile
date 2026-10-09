# syntax=docker/dockerfile:1
# Tafakor AI service image: Go backend + widget bundle + knowledge guide.
# Network downloads try the build's HTTP(S)_PROXY first, then a direct connection.

FROM node:22-slim AS widget
WORKDIR /w
COPY widget/package.json widget/package-lock.json ./
RUN npm ci --no-audit --no-fund \
    || env -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy npm ci --no-audit --no-fund
COPY widget/ ./
RUN npm run build

FROM golang:1.26-alpine AS api
# Module mirror is configurable (proxy.golang.org answers 403 for modernc.org on
# some networks); goproxy.cn is the default here for that reason.
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY service/go.mod service/go.sum ./
RUN go mod download \
    || env -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy go mod download
COPY service/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/aiservice ./cmd/aiservice

FROM alpine:3.22
RUN (apk add --no-cache ca-certificates tzdata \
     || env -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy apk add --no-cache ca-certificates tzdata) \
    && adduser -D -H -u 10002 ai && mkdir -p /data && chown ai:ai /data
COPY --from=api /out/aiservice /app/aiservice
COPY --from=widget /w/dist /app/widget
COPY knowledge /app/knowledge
ENV AI_DATA_DIR=/data AI_WIDGET_DIR=/app/widget AI_KNOWLEDGE_DIR=/app/knowledge
USER ai
VOLUME ["/data"]
EXPOSE 8090
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/app/aiservice", "healthcheck"]
ENTRYPOINT ["/app/aiservice"]
