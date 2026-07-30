# KeKeIO Tab Backend

KeKeIO Tab 的单机自托管后端，提供邮箱验证账号、`SharedProfile v2` 配置同步、资源目录和局域网运维工作台。SQLite 是唯一数据源；本地图片、图标 Blob、第三方凭据和设备运行状态不会上传。

## Docker 正式部署

正式后端统一监听容器端口 `9009`，并由 Caddy 提供严格路径白名单。小米路由器推荐使用 Cloudflare Tunnel：公网只进入 Caddy 的公网专用监听器，安装与后台使用独立的 LAN HTTPS 入口，后端端口不发布。

- [完整路由器部署指南](deploy/router/README.md)
- [Docker Compose](deploy/router/compose.yaml)
- [标准 Tunnel 覆盖](deploy/router/compose.tunnel.yaml)
- [SimpleDocker Tunnel 覆盖](deploy/router/compose.tunnel-simpledocker.yaml)
- [Tunnel 专用 Caddyfile](deploy/router/Caddyfile.tunnel)
- [环境变量样例](deploy/router/router.env.example)

```sh
# Git 仓库中：cd backend/deploy/router
# 后端 Release ZIP 解压后：cd deploy/router
cp router.env.example .env
# 填写固定镜像、持久目录、路由器 LAN 地址、精确管理 CIDR 与 Tunnel 文件路径
docker compose -p kekeio-tab --env-file .env \
  -f compose.yaml -f compose.tunnel-simpledocker.yaml config
```

### 离线 ARM64 Docker 镜像

从 Actions artifact 或 `v*` GitHub Release 下载完整的路由器包并先校验：

```sh
sha256sum -c kekeio-tab-router-arm64.tar.sha256
docker load -i kekeio-tab-router-arm64.tar
```

完整 tar 包含 `kekeio-tab:arm64`、`caddy:2.11.4-alpine` 和 `cloudflare/cloudflared:2026.7.3`。仅含应用的兼容包 `kekeio-tab-docker-arm64.tar` 仍可用 `docker load` 导入，但完全离线时必须另外准备 Caddy 与 cloudflared。

在 `deploy/router/.env` 中设置 `KEKEIO_IMAGE=kekeio-tab:arm64`，保持 `KEKEIO_TRUSTED_PROXIES=172.30.88.2/32`，并填写真实 LAN 地址、最小管理 CIDR、Docker bridge gateway 和受保护的 Token 文件路径。小米/SimpleDocker 启动命令为：

```sh
docker compose -p kekeio-tab --env-file .env \
  -f compose.yaml -f compose.tunnel-simpledocker.yaml \
  up -d --pull never
```

离线路径不登录 GHCR，也不执行 pull。新 Tunnel Token 只能通过本地只读文件挂载；不要放入命令、环境变量或 Git。`bin/fullpro-server-linux-arm64` 是非 Docker 场景的裸二进制，不能执行 `docker load -i bin/fullpro-server-linux-arm64`。

Tunnel 链路为：

```text
https://tab.kekeio.com -> Cloudflare Edge -> cloudflared -> Caddy :8081 -> backend:9009
```

Tunnel 只需出站连接，不做 WAN 端口转发。严禁使用 `--network container:...`，也不要把 Tunnel origin 设置为 backend 或 `localhost:9009`；这会绕过 Caddy 公网白名单和后端代理信任边界。

镜像以 UID/GID `10001` 非 root 用户运行。使用宿主机目录前必须预创建 `/data` 与 `/backups` 对应路径并 `chown 10001:10001`；优先使用 ext4 等支持 SQLite 锁、WAL、原子 rename 和 Unix 权限的本地文件系统。Docker 启动时会验证 `/backups` 可写并执行 `fsync`，失败直接退出。同一磁盘上的两个目录只提供逻辑隔离；灾备仍需第二介质或离机复制。

### 首次安装

首次启动会生成 128-bit 一次性安装码，同时输出到容器日志并写入 `/data/install-code`：

```sh
docker compose --env-file .env -f compose.yaml logs backend
```

Tunnel 模式先按完整指南导出并信任 Caddy 本地 CA，然后在允许的局域网中打开：

```text
https://<路由器LAN地址>:8443/install
```

安装向导会完成环境检查、独立管理员创建、公网 API、精确扩展来源、SMTP、配额及保留策略配置。公网 URL 填 `https://tab.kekeio.com`，不要追加 `:9009` 或 `:8443`；备份目录填 `/backups`。系统没有默认管理员账号或固定生产密码，安装完成后一次性安装码会失效并删除。

Tunnel 模式的后台入口是 `https://<路由器LAN地址>:8443/admin`；公网 `https://tab.kekeio.com/admin` 必须返回 404。Caddy 只允许 `.env` 中配置的精确 LAN/VLAN 来源访问 `/admin*`、`/install*` 和 `/api/admin/*`，后端还会再次执行管理员 CIDR 与 HTTPS 校验。任一公网管理路径不是 404 都必须停止上线。

### 路由器端口与 IPv4/IPv6

- Tunnel 只建立出站连接，不开放或转发任何 WAN 端口。
- `8080/8443` 只绑定路由器真实 LAN 地址；管理电脑通过 `8443` 和受信任的 Caddy 本地 CA 访问。
- SimpleDocker 兼容方案把 `18081` 只绑定到 Docker 默认 bridge gateway，并且该 Caddy 监听器只包含公网白名单。
- Tunnel DNS 记录由 Cloudflare 管理；删除绕过 Tunnel、直接指向家庭公网地址的旧 A/AAAA。
- 只有明确停用 Tunnel、改回直连模式时，才按完整指南配置标准 WAN `80/443`、证书与防火墙。

### 反向代理路径与可信代理

公网只允许：

```text
/
/api/v1/*
/account/verify
/account/reset
/account/assets/*
/health/live
/health/ready
```

`/account/assets/*` 是邮箱验证与密码重置页面所需的 CSS/JS，遗漏会让账号流程失效。未匹配路径统一返回 404。

默认 Compose 不发布后端宿主端口。现有代理在另一个容器中时应加入同一 Docker 网络并使用 `backend:9009`；宿主机代理必须显式叠加 `compose.host-proxy.yaml` 才能使用 `127.0.0.1:9009`。只有直接对端位于 `FULLPRO_TRUSTED_PROXIES` 时后端才读取 `X-Forwarded-For`/`X-Forwarded-Proto`，因此 `KEKEIO_TRUSTED_PROXIES` 必须收窄到代理容器 IP 或实际 bridge gateway `/32`，不能照抄任意私网段。完整命令与旧版 Docker 的 localhost 发布风险见路由器部署指南。

## 生产环境变量

```text
FULLPRO_ADDR=:9009
FULLPRO_DB=/data/fullpro.db
FULLPRO_BACKUP_DIRECTORY=/backups
FULLPRO_SECRETS_FILE=/data/secrets.json
FULLPRO_INSTALL_CODE_FILE=/data/install-code
FULLPRO_INSTALL_CODE=<可选的显式一次性安装码>
FULLPRO_COOKIE_NAME=fullpro_session
FULLPRO_INSTALL_COOKIE_NAME=fullpro_install
FULLPRO_COOKIE_SECURE=true
FULLPRO_PUBLIC_BASE_URL=https://tab.kekeio.com
FULLPRO_ADMIN_ALLOWED_CIDRS=127.0.0.1/32,::1/128,192.168.50.0/24
FULLPRO_TRUSTED_PROXIES=<Caddy容器IP>/32
FULLPRO_AUTH_RATE_LIMIT=20
FULLPRO_AUTH_RATE_WINDOW_SECONDS=60
FULLPRO_PASSWORD_HASH_CONCURRENCY=2
FULLPRO_HEALTHCHECK_URL=http://127.0.0.1:9009/health/live
```

`FULLPRO_BACKUP_DIRECTORY` 是启动时强制覆盖项，优先级高于安装向导保存的备份目录；后端会在开始监听前验证该目录可写并执行 `fsync`。Docker 正式部署应保持为 `/backups`，不要再通过向导改到容器外不可见的宿主路径。

`FULLPRO_PASSWORD_HASH_CONCURRENCY` 限制进程内 Argon2id 创建与校验的并发数，默认 `2`、允许 `1..16`；在内存受限路由器上保持较小值。

安装向导保存的公网 URL、精确扩展/Web Origin、注册开关、SMTP、配额和保留策略会持久化。只有需要在每次启动强制覆盖数据库值时才设置：

```text
FULLPRO_REGISTRATION_OPEN=true
FULLPRO_API_ALLOWED_ORIGINS=chrome-extension://<扩展ID>,https://app.example.com
```

Origin 必须精确匹配；后端不会默认放行任意 `chrome-extension://` 来源。设置 `FULLPRO_API_ALLOWED_ORIGINS` 会替换安装向导保存的来源列表，正常生产部署应留空并通过向导管理。

## 管理员恢复

管理员密码遗失时，停止后端后在相同数据卷上运行恢复命令：

```sh
docker compose --env-file .env -f compose.yaml stop backend
docker compose --env-file .env -f compose.yaml rm -f backend
docker compose --env-file .env -f compose.yaml run --rm --no-deps backend fullpro-server admin-reset
docker compose --env-file .env -f compose.yaml up -d backend
```

删除停止状态的容器是为了释放固定的 Docker IP，不会删除 bind mount 数据。恢复命令会撤销现有管理员会话、记录本地恢复审计、将安装入口切换为仅管理员重置模式，并生成新的一次性恢复码；不会删除用户数据。继续通过局域网 HTTPS 安装入口完成重置。

## 健康检查

```text
GET /health/live   # 进程存活；不访问数据库
GET /health/ready  # SQLite、schema、安装和维护/恢复门禁
GET /healthz       # /health/live 的兼容别名
```

`/health/live` 和 `/healthz` 正常时返回 `200 {"status":"ok"}`。`/health/ready` 仅在数据库可查询、migration 与当前程序完全匹配、安装状态为 `installed`，且没有 `running` 维护任务或 `restoring` 备份时返回 `200 {"status":"ready"}`。失败返回 503，`reason` 只会是 `database`、`schema`、`installation` 或 `maintenance`，不会暴露内部错误。健康请求不会写入 HTTP 访问日志，所有响应均禁止缓存。

Docker 镜像默认用 `/health/live` 作为 `HEALTHCHECK`，因此首次安装和维护期间仍能正确表示进程存活；启动前还会单独验证备份目录可写。外部监控或编排系统应使用 `/health/ready` 判断是否可以接收业务流量。若覆盖 `FULLPRO_ADDR`，必须同步覆盖 `FULLPRO_HEALTHCHECK_URL`。

## 非生产本地模式

`go run ./cmd/fullpro-server dev` 仍固定监听 `127.0.0.1:8787`，使用隔离的 `.dev-data` 和固定测试账号，仅供本地开发调试。它不会使用生产安装配置或 `/data`，不能用于 Docker 或正式部署。

## 备份、升级与回滚

- `data-only` 备份保存一致的 SQLite 快照；恢复时会生成全新 secrets，并撤销快照中的管理员会话、access/refresh token 和旧插件会话。
- `full` 备份额外包含 secrets，必须输入至少 12 字符的恢复口令；secrets 以 Argon2id 派生密钥和 AES-256-GCM 加密，口令不会保存。完整恢复同样撤销所有快照会话，要求用户重新登录。
- 服务启动后立即创建一次自动 `data-only` 备份，此后每 24 小时执行；自动备份保留最近 7 个日恢复点和更早 4 个周恢复点，手动备份不受自动清理影响。手动、自动和迁移前快照均使用 SQLite online-backup API。
- 恢复前会校验 manifest、每个文件的 SHA-256、SQLite `quick_check`、外键及 schema；旧版本备份先在 staged 副本上迁移并复验。服务先排空 HTTP 与定时维护，再依据持久 restore journal 替换数据库与 secrets；任一步崩溃都会在下次打开数据库前恢复到确定的完整文件配对。
- 数据库需要升级时，服务会先生成 `fullpro.db.pre-migration-v<版本>-<内容摘要>.sqlite` 一致性快照，再在单一 SQLite 事务中迁移。未来版本或不连续 migration history 会在写库前拒绝；迁移失败时原库保持不变且服务拒绝启动。
- 文件数据库在迁移完成后启用 SQLite WAL 与 `synchronous=NORMAL`；每日 retention 清理 24 小时幂等响应、90 天 mutation/device 证据及已过期或撤销的会话和一次性 token。

升级前仍建议主动创建一份完整备份并保存恢复口令。需要回滚二进制时，先停止服务，再使用与目标版本兼容的迁移前快照或后台备份；不要让两个版本同时打开同一 SQLite 文件。

## Canonical API

插件端：

```text
POST /api/v1/auth/register
POST /api/v1/auth/verify-email
POST /api/v1/auth/resend-verification
POST /api/v1/auth/login
POST /api/v1/auth/refresh
POST /api/v1/auth/logout
POST /api/v1/auth/forgot-password
POST /api/v1/auth/reset-password
GET  /api/v1/me

GET  /api/v1/sync/profile
PUT  /api/v1/sync/profile
GET  /api/v1/sync/profile/versions
POST /api/v1/sync/profile/versions/{id}/restore

GET  /api/v1/app/bootstrap
GET  /api/v1/catalog/wallpapers/official
GET  /api/v1/catalog/wallpapers/web
GET  /api/v1/catalog/styles
```

后台端统一位于 `/api/admin/v1/*`，采用 HttpOnly、SameSite=Strict 管理员 Cookie；状态变更还要求精确 Origin、`X-CSRF-Token` 和 `application/json`。后台提供用户与同步诊断、内容草稿/校验/发布/回滚、版本公告、审计、运行设置、维护和备份恢复。

成功响应统一为：

```json
{"data": {}, "requestId": "req_..."}
```

错误响应统一为：

```json
{"error": {"code": "...", "message": "..."}, "requestId": "req_..."}
```

## 验证

在仓库根目录执行：

```powershell
.\scripts\verify-all.ps1
```

该脚本会依次运行 Go 测试与 `go vet`、后台 UI 测试与生产构建、扩展测试与类型检查/生产构建，以及跨模块安全基线检查。
