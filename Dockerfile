# 使用仓库现有固定工具链；兼容 Jenkins 的传统 Docker 构建方式。
FROM golang:1.27.0-alpine3.24@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc AS go-toolchain

FROM node:25-alpine3.23@sha256:bdf2cca6fe3dabd014ea60163eca3f0f7015fbd5c7ee1b0e9ccb4ced6eb02ef4 AS builder
COPY --from=go-toolchain /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}" CGO_ENABLED=1
WORKDIR /build
RUN apk add --no-cache bash make gcc musl-dev git ca-certificates

# 先安装锁定依赖，源码变动时可复用依赖层。
COPY ui/package.json ui/package-lock.json ./ui/
RUN cd ui && npm ci
COPY ui/ ./ui/
COPY ee/ ./ee/
COPY core/ ./core/
COPY framework/ ./framework/
COPY plugins/ ./plugins/
COPY transports/ ./transports/

ARG VERSION=dev
RUN case "$VERSION" in \
      ''|[!A-Za-z0-9_]*|*[!A-Za-z0-9_.-]*) echo '版本号格式不合法' >&2; exit 1 ;; \
    esac \
    && test "${#VERSION}" -le 50 \
    && mkdir -p ee/cmd/bifrost-http/ui \
    && make -C ee build VERSION="$VERSION" \
    && test -s ee/cmd/bifrost-http/ui/index.html \
    && test -x ee/tmp/bifrost-http

FROM alpine:3.23.5@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40
WORKDIR /app
RUN apk add --no-cache musl libgcc ca-certificates zlib \
    && adduser -D -u 1000 -s /bin/sh appuser \
    && mkdir -p /app/data/logs \
    && chown -R 1000:0 /app/data \
    && chmod -R g=rwX /app/data
COPY --from=builder /build/ee/tmp/bifrost-http /app/main
COPY --from=builder /build/transports/docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod 755 /app/main /app/docker-entrypoint.sh

ARG VERSION=dev
ARG VCS_REF=unknown
LABEL org.opencontainers.image.source="ssh://git@gitlab.zxiaowo.com:10022/ai-tool/ai-gateway.git" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$VCS_REF"
ENV APP_HOST=0.0.0.0 APP_PORT=8080 APP_DIR=/app/data LOG_LEVEL=info LOG_STYLE=json
USER 1000:0
VOLUME ["/app/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:${APP_PORT}/health" || exit 1
ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["/app/main"]
