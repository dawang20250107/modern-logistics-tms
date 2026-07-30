# 多阶段构建：编译出静态二进制，运行镜像只留二进制 + 字体 + CA。
# 迁移与内嵌资源（弱口令表、SQL、水印字体探测）都在二进制里，运行期无需源码。
FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/adminctl ./cmd/adminctl \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/fontcheck ./cmd/fontcheck

FROM alpine:3.20
# 字体装 noto-cjk 而不是 wqy-zenhei：后者的 .ttc 被 x/image/font/sfnt 解析会报
# invalid table offset（fc-list 能列出、Pillow 也能用，但我们这条链路用不了），
# 装了等于没装，打卡照片会静默不带水印——水印没了就是现场证据没了。
RUN apk add --no-cache ca-certificates tzdata font-noto-cjk \
    && adduser -D -u 10001 tms
ENV TZ=Asia/Shanghai
WORKDIR /app
COPY --from=build /out/gateway /app/gateway
# 开首个超管要用（docker compose exec gateway /app/adminctl -u admin -p '...'）；
# schema 由网关启动时内嵌迁移器建立，故 cmd/migrate 不入镜像
COPY --from=build /out/adminctl /app/adminctl
# 构建期就把「这个镜像能渲染中文水印」验死。水印失效是静默的：没字体照样打卡成功，
# 只是照片从此不带时间/GPS/业务上下文，等要拿它当证据时才发现就晚了。
COPY --from=build /out/fontcheck /tmp/fontcheck
RUN /tmp/fontcheck && rm /tmp/fontcheck
# 媒体目录挂卷进来；镜像里只留一个空挂载点
RUN mkdir -p /app/media && chown -R tms:tms /app
USER tms
EXPOSE 8000
HEALTHCHECK --interval=10s --timeout=3s --retries=10 --start-period=15s \
  CMD wget -qO- http://127.0.0.1:8000/healthz || exit 1
ENTRYPOINT ["/app/gateway"]
