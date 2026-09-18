# 前后端由 deploy/Jenkinsfile 编译；此文件只封装运行镜像。
FROM registry.cn-hangzhou.aliyuncs.com/yxdocker/alpine:3.23.5-amd64
WORKDIR /app
RUN apk add --no-cache musl libgcc ca-certificates zlib \
    && adduser -D -u 1000 -s /bin/sh appuser \
    && mkdir -p /app/data/logs \
    && chown -R 1000:0 /app/data \
    && chmod -R g=rwX /app/data
# Jenkins 在复制前设置 755 权限，避免传统构建器 chmod 再生成完整二进制层。
COPY .jenkins-artifacts/main /app/main
COPY .jenkins-artifacts/docker-entrypoint.sh /app/docker-entrypoint.sh

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
