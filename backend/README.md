# KeKeIO Tab Backend

KeKeIO Tab 的单机自托管后端，提供邮箱验证账号、`SharedProfile v2` 配置同步、资源目录和局域网运维工作台。SQLite 是唯一数据源；本地图片、图标 Blob、第三方凭据和设备运行状态不会上传。

## 本地快速测试

本机调试不需要配置环境变量或走安装向导：

```powershell
cd .\backend
go run ./cmd/fullpro-server dev
```

该命令固定监听 `127.0.0.1:8787`，首次运行会在 `.dev-data` 下生成独立的数据库和 secrets，并自动创建管理员账号 `admin@local.test` 与插件账号 `user@local.test`；两者密码固定为 `2231`。下次 `dev` 启动会自动将旧的本地测试账号重置为这个密码。后台入口是 `http://127.0.0.1:8787/admin`。正式扩展固定连接 `https://tab.kekeio.com`，本地 `dev` 模式用于快速验证后台与 API；端到端扩展同步需由该域名的 HTTPS 代理转发到后端。

快捷模式仅允许 loopback 连接，并且仅在此模式下为格式正确的 Chrome 扩展 Origin 自动放行 CORS。它不会使用生产启动与安装配置，也不会使用 `data/`，不能用于 Docker 或正式部署。需要重新生成本地数据时，先停止该进程，再删除 `.dev-data` 后重新执行命令。

## 首次启动

```powershell
cd .\backend
go run ./cmd/fullpro-server
```

首次启动会生成 128-bit 一次性安装码，并同时输出到日志及数据目录下权限受限的 `install-code` 文件。随后在服务器本机打开：

```text
http://127.0.0.1:8787/install
```

安装向导会完成环境检查、独立管理员创建、公网 API、扩展来源、SMTP、配额及保留策略配置。系统没有默认管理员账号或固定密码；安装完成后一次性安装码会失效并删除。

后台入口：

```text
http://127.0.0.1:8787/admin
```

除严格 loopback 开发访问外，后台和安装页必须经 HTTPS 访问，并且只接受 `FULLPRO_ADMIN_ALLOWED_CIDRS` 允许的网络。公网只应暴露 `/api/v1/*` 插件 API。

## 环境变量

```text
FULLPRO_ADDR=:8787
FULLPRO_DB=/data/fullpro.db
FULLPRO_SECRETS_FILE=/data/secrets.json
FULLPRO_INSTALL_CODE_FILE=/data/install-code
FULLPRO_INSTALL_CODE=<可选的显式一次性安装码>
FULLPRO_COOKIE_NAME=fullpro_session
FULLPRO_INSTALL_COOKIE_NAME=fullpro_install
FULLPRO_COOKIE_SECURE=true
FULLPRO_ADMIN_ALLOWED_CIDRS=127.0.0.1/32,::1/128,192.168.0.0/16
FULLPRO_TRUSTED_PROXIES=127.0.0.1/32,::1/128
FULLPRO_AUTH_RATE_LIMIT=20
FULLPRO_AUTH_RATE_WINDOW_SECONDS=60
FULLPRO_PASSWORD_HASH_CONCURRENCY=2
FULLPRO_HEALTHCHECK_URL=http://127.0.0.1:8787/health/ready
```

`FULLPRO_PASSWORD_HASH_CONCURRENCY` 限制进程内 Argon2id 创建与校验的并发数，默认 `2`、允许 `1..16`；在内存受限设备上保持较小值，避免并发登录或注册耗尽内存。

安装向导保存的公网 URL、精确扩展/Web Origin 白名单、注册开关、SMTP、配额和保留策略会在安装完成后立即生效，并在重启时自动加载。以下环境变量仅用于显式覆盖持久化运行配置：

```text
FULLPRO_PUBLIC_BASE_URL=https://tab.kekeio.com
FULLPRO_REGISTRATION_OPEN=true
FULLPRO_API_ALLOWED_ORIGINS=chrome-extension://<扩展ID>,https://app.example.com
```

Origin 必须精确匹配；后端不会默认放行任意 `chrome-extension://` 来源。只有直接对端位于 `FULLPRO_TRUSTED_PROXIES` 时才读取转发头，配置反向代理时应同时传递可信的 `X-Forwarded-Proto` 和 `X-Forwarded-For`。

## 管理员本地恢复

管理员密码遗失时，在服务器本机停止服务后执行：

```powershell
go run ./cmd/fullpro-server admin-reset
```

该命令会撤销现有管理员会话、记录本地恢复审计、将安装入口切换为仅管理员重置模式，并生成新的 128-bit 一次性恢复码。使用日志或 `FULLPRO_INSTALL_CODE_FILE` 中的恢复码完成向导；不会重新开放全新安装，也不会删除用户数据。

## Docker

```bash
docker buildx build --platform linux/arm64 -t kekeio-tab:latest .
docker run -d \
  --name kekeio-tab \
  --restart unless-stopped \
  -p 127.0.0.1:8787:8787 \
  -v /mnt/usb-24aeefbb/kekeio-tab:/data \
  -e FULLPRO_ADDR=:8787 \
  -e FULLPRO_DB=/data/fullpro.db \
  -e FULLPRO_COOKIE_SECURE=true \
  -e FULLPRO_PUBLIC_BASE_URL=https://tab.kekeio.com \
  kekeio-tab:latest
```

建议由 Caddy 或 Nginx 终止 HTTPS。不要把 `/admin`、`/install` 或 `/api/admin/v1/*` 暴露到公网。

以下是同机 Caddy 的最小公网配置。`route` 保证未匹配插件 API 或健康端点的请求直接返回 404；Caddy 会自动设置 `X-Forwarded-For` 和 `X-Forwarded-Proto`：

```caddyfile
tab.kekeio.com {
  route {
    @public path /api/v1/* /account/verify /account/reset /health/live /health/ready
    reverse_proxy @public 127.0.0.1:8787
    respond 404
  }
}
```

### 路由器 DDNS 与 IPv4/IPv6

`ddns-go` 中可同时为 `tab.kekeio.com` 更新 A 和 AAAA。发布双栈记录时，两条链路必须同时可用：

- IPv4：把公网 TCP 443 端口转发到运行 Caddy/Nginx 的设备。
- IPv6：通常不做 NAT，但必须在路由器防火墙中放行到反向代理设备的 TCP 443；AAAA 应使用可公网路由的 IPv6 地址，而不是 `fc00::/7` ULA 地址。
- A 与 AAAA 必须到达同一套反向代理规则。任意一条记录失效，都可能导致部分客户端连接失败或证书签发失败。
- 反向代理负责 `tab.kekeio.com` 的有效公网证书。可开放 TCP 80/443 使用自动 ACME，或在只开放 443 时使用受支持的 TLS-ALPN/DNS 验证方案。
- 若反向代理运行在另一个 Docker 容器中，`127.0.0.1:8787` 指向的是代理容器自身；应把两个容器加入同一网络并使用后端服务名和 `8787` 端口。

公网只转发上述插件 API、账号验证/重置页和健康端点，继续阻断 `/admin`、`/install` 与 `/api/admin/v1/*`。在无图形界面的路由器上可通过 SSH 隧道进入本机安装/管理入口，例如 `ssh -L 8787:127.0.0.1:8787 root@<路由器地址>`，然后在电脑打开 `http://127.0.0.1:8787/install`。

同机代理应配置 `FULLPRO_TRUSTED_PROXIES=127.0.0.1/32,::1/128`。若 Caddy 位于容器网络或另一台主机，只加入其实际、受控的源地址或最小 CIDR，不要信任任意代理来源。

## 健康检查

```text
GET /health/live   # 进程存活；不访问数据库
GET /health/ready  # SQLite、schema、安装和维护/恢复门禁
GET /healthz       # /health/live 的兼容别名
```

`/health/live` 和 `/healthz` 正常时返回 `200 {"status":"ok"}`。`/health/ready` 仅在数据库可查询、migration 与当前程序完全匹配、安装状态为 `installed`，且没有 `running` 维护任务或 `restoring` 备份时返回 `200 {"status":"ready"}`。失败返回 503，`reason` 只会是 `database`、`schema`、`installation` 或 `maintenance`，不会暴露内部错误。健康请求不会写入 HTTP 访问日志，所有响应均禁止缓存。

Docker 镜像默认用 `/health/ready` 作为 `HEALTHCHECK`。首次安装完成前、维护任务运行中或备份恢复排队后显示 `unhealthy` 是预期的部署门禁；此时可用 `/health/live` 区分“进程仍存活”和“尚不可接流量”。覆盖 `FULLPRO_ADDR` 的端口时，也要把 `FULLPRO_HEALTHCHECK_URL` 改为容器内可访问的新地址。

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
