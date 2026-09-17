# 使用仓库现有固定工具链；兼容 Jenkins 的传统 Docker 构建方式。
FROM registry.cn-hangzhou.aliyuncs.com/yxdocker/golang:1.27.0-alpine3.24-amd64 AS go-toolchain

FROM registry.cn-hangzhou.aliyuncs.com/yxdocker/node:25-alpine3.23-amd64 AS builder

ENV GOPROXY=https://goproxy.cn
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

FROM registry.cn-hangzhou.aliyuncs.com/yxdocker/alpine:3.23.5-amd64
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
