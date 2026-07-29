# 多阶段构建：编译出静态二进制，运行镜像只留二进制 + 字体 + CA。
# 迁移与内嵌资源（弱口令表、SQL、水印字体探测）都在二进制里，运行期无需源码。
FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/adminctl ./cmd/adminctl

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata font-wqy-zenhei \
    && adduser -D -u 10001 tms
ENV TZ=Asia/Shanghai
WORKDIR /app
COPY --from=build /out/gateway /app/gateway
# 开首个超管要用（docker compose exec gateway /app/adminctl -u admin -p '...'）；
# schema 由网关启动时内嵌迁移器建立，故 cmd/migrate 不入镜像
COPY --from=build /out/adminctl /app/adminctl
# 媒体目录挂卷进来；镜像里只留一个空挂载点
RUN mkdir -p /app/media && chown -R tms:tms /app
USER tms
EXPOSE 8000
HEALTHCHECK --interval=10s --timeout=3s --retries=10 --start-period=15s \
  CMD wget -qO- http://127.0.0.1:8000/healthz || exit 1
ENTRYPOINT ["/app/gateway"]
