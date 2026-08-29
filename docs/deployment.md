# 部署指南（本地预演 → 腾讯云）

> 本文在 Django 退役后重写过一次。旧版描述的是 gunicorn + Celery + Redis 的形态，
> 那套东西已经不存在了——照着旧文档部署会去找几个根本没有的服务。

## 架构

```
客户端 ──→ Nginx（web 容器, :80）
              ├─ /            前端 SPA（静态文件）
              └─ /api/*       反代到 gateway:8000
gateway（唯一的应用进程，Go 单二进制）
              ├─ 全部 /api/v1 路由
              ├─ /media/*     用户上传物直出（nosniff、不开目录列表）
              ├─ schema 迁移  启动时内嵌执行
              └─ 进程内后台：轨迹削峰落库 / 掉线扫描 / 指标物化 /
                            MQTT 订阅 / 令牌黑名单清理
              ↓
        PostgreSQL 16
```

没有 Redis、没有 Celery、没有单独的 worker/beat。需要横向扩容时 gateway
可以多副本（无状态），但**进程内的定时任务会在每个副本上各跑一份**——
指标物化与掉线扫描是幂等的，多跑无害；真要精确控制时把它们拆成
单副本的 CronJob。

## 本地生产预演

```bash
cp deploy/.env.prod.example deploy/.env.prod
# 至少要改 DJANGO_SECRET_KEY / POSTGRES_PASSWORD / DJANGO_CORS_ORIGINS / PUBLIC_BASE_URL
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.prod up -d --build

# 开第一个超管（Django 的 createsuperuser 已随之退役）
docker compose -f deploy/docker-compose.prod.yml exec gateway /app/adminctl -u admin -p '<强口令>'

# 可选：铺一份演示数据（幂等，可反复跑；-reset 清掉重来）
docker compose -f deploy/docker-compose.prod.yml exec gateway /app/seed
```

访问 <http://127.0.0.1/>。探针：`/healthz`（存活）、`/readyz`（就绪，会 ping 库）。

> 生产编排用 80 端口，开发编排（`docker-compose.yml`）用别的；
> 两者不要同时挂同一个 DB 卷。

## 启动前置检查

`DJANGO_DEBUG=false` 时，网关在监听之前会自检配置，不通过就**拒绝启动**：

| 检查 | 为什么是致命的 |
|---|---|
| `DJANGO_SECRET_KEY` 非空、≥32 字节、不是已知占位值 | 这把钥匙同时签发内部用户令牌与司机端令牌。留着 `CHANGE-ME-IN-PRODUCTION...` 等于任何人都能自签一张管理员令牌进来，而这类问题在测试环境永远不会暴露——那里本来就用默认值 |
| `DJANGO_CORS_ORIGINS` 不含 `*` | 通配符 + 带凭证的跨站请求 = 任意站点可代表已登录用户发请求 |

另有几条只警告不阻断（`PUBLIC_BASE_URL` 还是 127.0.0.1、CORS 仍是开发默认值、
生产开着自助注册），会在启动日志里以 `配置提醒` 打出来。

## 关键环境变量

| 变量 | 必设 | 说明 |
|---|:--:|---|
| `DJANGO_SECRET_KEY` | ✅ | 强随机 ≥32 字节。`openssl rand -base64 48` |
| `DATABASE_URL` | ✅ | 托管 PG 时换成云上地址即可，无需改代码 |
| `DJANGO_CORS_ORIGINS` | ✅ | 前端来源，逗号分隔，写明确不写 `*` |
| `PUBLIC_BASE_URL` | ✅ | 对外可访问的基址；头像/回单的绝对地址靠它拼 |
| `DJANGO_DEBUG` | ✅ | 生产必须 `false`，否则前置检查整个跳过 |
| `TMS_ALLOW_SELF_REGISTRATION` | | 默认 `0`（关）。账号应由管理员在「组织与权限 → 员工名录」开通，开出来就带组织与角色。仅对外客户门户需要自助开户时置 `1` |
| `MEDIA_ROOT` | | 上传物落盘根目录，容器里挂卷 |
| `DEEPSEEK_API_KEY` | | 不配则 AI 相关端点返回 503，其余功能不受影响 |
| `MQTT_HOST` | | 不配则不启用车载终端 MQTT 接入 |
| `AMAP_KEY` | | 地图/地理编码 |

## 首次上线检查清单

- [ ] `.env.prod` 里的密钥与口令都换成强随机，且**不在版本库里**
- [ ] 前置网关（Nginx / CLB）终止 TLS，80 跳 443
- [ ] `docker compose ps` 里 gateway 是 `healthy`（它探的是 `/readyz`，会 ping 库）
- [ ] 用 `/app/adminctl` 开的超管能登录，随后**立刻改掉命令行里用过的口令**
  （命令行参数会进 shell history 与进程列表）
- [ ] 跑过 `seed` 的话，把 `seed_*` 四个演示账号删掉——它们是公开的弱口令
- [ ] 备份跑起来了（见下节），并且**至少演练过一次恢复**

## 备份与恢复

系统的全部状态在两个地方：**PostgreSQL** 与 **媒体卷**（回单、证件、
打卡照片、合同扫描件）。备份必须同时覆盖两者——只备库的话，
对账单还在但作为凭证的回单照片没了，等于账对不上还拿不出证据。

### 备份

```bash
# 数据库（自定义格式，支持并行恢复与按表挑选）
docker compose -f deploy/docker-compose.prod.yml exec -T db \
  pg_dump -U tms -d tms -Fc > backup/tms-$(date +%F-%H%M).dump

# 媒体卷
docker run --rm -v deploy_media:/media -v "$PWD/backup:/backup" alpine \
  tar czf /backup/media-$(date +%F-%H%M).tar.gz -C /media .
```

建议每日一次全量 + 异地留存。托管 PG（TencentDB）自带自动备份与 PITR，
用托管的就不必自己跑 `pg_dump`，但**媒体卷仍需单独备份**——
它不在数据库里。

### 恢复演练

没演练过的备份等于没有备份。至少走一遍：

```bash
# 1) 起一个干净的库
docker compose -f deploy/docker-compose.prod.yml down
docker volume rm deploy_pgdata
docker compose -f deploy/docker-compose.prod.yml up -d db

# 2) 恢复
docker compose -f deploy/docker-compose.prod.yml exec -T db \
  pg_restore -U tms -d tms --clean --if-exists < backup/tms-<时间戳>.dump

# 3) 媒体卷
docker run --rm -v deploy_media:/media -v "$PWD/backup:/backup" alpine \
  sh -c 'rm -rf /media/* && tar xzf /backup/media-<时间戳>.tar.gz -C /media'

# 4) 起网关，确认 /readyz 200、能登录、随便点开一张运单能看到回单图
docker compose -f deploy/docker-compose.prod.yml up -d
curl -f http://127.0.0.1:8000/readyz
```

第 4 步的「能看到回单图」不是可选项——它是唯一能证明**两份备份对得上**
的检查。库恢复到 T1、媒体恢复到 T2 的话，T1 到 T2 之间的单据会指向不存在的文件。

### schema 升级

网关启动时自动跑内嵌迁移器（只前进不回滚）。所以顺序是：
**先备份 → 再滚动新版本**。迁移失败会导致启动失败并退出，
容器不会带着半吊子 schema 对外服务。

## 迁移到腾讯云

### 方案 A：CVM 单机（最快）

1. CVM 装 Docker + Compose，安全组放行 80/443。
2. 拉代码，写 `deploy/.env.prod`。
3. 建议改用托管服务：TencentDB for PostgreSQL + COS 对象存储 ——
   只改 `DATABASE_URL` 与存储配置，不改代码。
4. `docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.prod up -d --build`
5. 前置 CLB 终止 TLS。

### 方案 B：TKE（Kubernetes）

1. 镜像推 TCR：`tms-gateway:prod`、`tms-frontend:prod`。
2. gateway Deployment 多副本（无状态，HPA 按 CPU/QPS）。
   注意进程内定时任务会每副本各跑一份，见「架构」一节。
3. 托管 PostgreSQL（读写分离）。
4. **媒体必须接对象存储**——多副本下这是硬性前提，见下节。
5. Ingress 路由 + TLS。
6. 探针：`livenessProbe` → `/healthz`，`readinessProbe` → `/readyz`。
   **两者不能都用 `/healthz`**：它恒回 200，连不上库的副本会被判定健康、
   继续接流量，每个请求都 500。
7. 配置经 Secret 注入；JSON 结构化日志采集到 CLS。

## 媒体存放（多副本前必须处理）

媒体文件指头像、司机证件、打卡照、回单、合同 PDF。

### 为什么这是多副本的硬性前提

默认 `MEDIA_BACKEND=local`，文件写在网关容器的本地盘上。单副本没问题；
**一旦起两个副本就坏了**：上传落在 A 的盘上，下一次 GET 被负载均衡路由到 B，
B 上没有这个文件，404。

这个故障有两个特征让它特别难查：

- **间歇性**。同一张图刷新几次可能好几次坏几次，取决于路由到哪个副本。
  报上来的现象往往是"图片有时候显示不出来"，很容易被当成网络问题。
- **预发复现不出来**。预发通常单副本，那里一切正常。

所以它会一路活到生产扩容那一刻，然后同时影响所有历史上传的文件。

### 怎么配

```
MEDIA_BACKEND=s3
S3_ENDPOINT=https://cos.ap-shanghai.myqcloud.com
S3_REGION=ap-shanghai
S3_BUCKET=tms-media-prod
S3_ACCESS_KEY=...
S3_SECRET_KEY=...
S3_PATH_STYLE=0        # MinIO / 多数自建网关置 1
```

支持任何 S3 兼容存储：AWS S3、腾讯云 COS、阿里云 OSS 的 S3 网关、MinIO。

**填了 `s3` 但少配任何一项，网关会拒绝启动**，不会退回本地盘。这是有意的：
退回去会让多副本"看起来正常"地跑起来然后间歇丢文件，比起不来难查得多。

### 读为什么仍然走网关

接了对象存储之后，`/media/<key>` 仍由网关读出来再吐给客户端，
而不是 302 到预签名 URL。媒体里有身份证、行驶证、签收回单——
这些是能直接看到人脸和签名的东西。让它们只经过一个出口，
将来要加鉴权、加水印、加访问审计都只有一处要改。

代价是网关在数据通路上。媒体流量大到成为瓶颈时再换 302，那是另一个问题。

### 已有文件的迁移

切到 s3 之前，把现有 `media/` 目录整个同步进桶，保持相对路径不变
（库里存的就是这个相对路径，三处必须一致）：

```bash
# 以 coscli 为例；aws s3 sync / mc mirror 同理
coscli sync ./media/ cos://tms-media-prod/ --recursive
```

同步完再改 `MEDIA_BACKEND` 重启。**先同步后切换**——反过来的话，
切换之后到同步完成之间上传的文件会留在旧副本的本地盘上，之后再也找不到。

### 尚未验证的一点

S3 那条实现是自己签的 SigV4（不引 aws-sdk-go-v2，为了少一大堆依赖）。
单元测试覆盖了请求形状、路径转义、签名输入完整性、错误不泄漏内网信息，
并用一个假 S3 跑通了网关的完整读写链路。

但**没有对着真实 S3 端点验过互操作性**——开发环境没有凭据。
首次接入时请先在预发用一个测试桶跑一遍上传与读取，确认签名被接受，
再切生产。

## 容量与压测

上线前用 `deploy/loadtest/k6-smoke.js` 对**扩缩容后的部署**压测，
校准副本数与 DB 规格。写热点（轨迹上报）走进程内有界队列削峰，
关注队列积压与批量落库速率。
