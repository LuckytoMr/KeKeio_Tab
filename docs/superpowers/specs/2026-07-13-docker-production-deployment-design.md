# KeKeIO Tab Docker 生产部署设计

## 目标与边界

- 后端生产监听端口统一为 `8881`，公网地址保持 `https://tab.kekeio.com`。
- Docker 是后端唯一正式部署方式；本地 `dev` 模式不参与本次生产验收。
- 路由器只把公网标准端口送到 HTTPS 反向代理，Go 后端端口不直接暴露到公网。
- GitHub Actions 和镜像构建统一使用 Node.js 24 LTS，并升级所有 JavaScript Action 到声明 Node.js 24 运行时的版本。
- 保留扩展 ZIP 与 GitHub Release 链路；Docker 仅替代后端部署方式，不替代浏览器扩展分发。

## 方案选择

### 采用：Docker Compose + Caddy

Compose 同时运行 `backend` 和 `caddy`。后端只在 Docker 网络的 `8881` 提供 HTTP；Caddy 负责证书、HTTPS、请求路径白名单和转发头。为避开路由器管理页面占用宿主机 `80/443`，Caddy 默认发布到宿主机 `8080/8443`，再由路由器执行：

- 公网 TCP `80` → Docker 主机 TCP `8080`
- 公网 TCP `443` → Docker 主机 TCP `8443`
- 可选公网 UDP `443` → Docker 主机 UDP `8443`，用于 HTTP/3

客户端仍使用标准 URL `https://tab.kekeio.com`，内部端口转换对 DNS、扩展和浏览器透明。

### 兼容：已有反向代理

如果路由器已有 Caddy、Nginx 或 Nginx Proxy Manager，可只运行后端。容器代理加入同一网络后使用 `backend:8881`；只有显式叠加 `compose.host-proxy.yaml` 时，宿主机代理才能使用 `127.0.0.1:8881`。代理必须覆盖客户端伪造的转发头并传递可信的 `X-Forwarded-For` 和 `X-Forwarded-Proto`。

### 不采用：公网 80 直接转发到 8881

该方式只能提供纯 HTTP，无法满足正式扩展固定 HTTPS、安全 Cookie、账号验证/重置链接和证书要求，因此不作为受支持的生产部署。

## 组件与数据流

1. `tab.kekeio.com` 的 A 记录指向路由器公网 IPv4。正式扩展编译时固定该 origin；只有同步重建扩展的 fork 才能使用其他域名。
2. 路由器把公网 `80/443` 转发到 Caddy 的宿主机映射端口。
3. Caddy 自动申请并续期证书，只转发以下公网路径：
   - `/`（改写为 `/health/live`）
   - `/api/v1/*`
   - `/account/verify`
   - `/account/reset`
   - `/account/assets/*`
   - `/health/live` 与 `/health/ready`
4. 公网根路径转发到后端 liveness，允许的管理网段访问根路径时跳转到 `/admin`；`/admin*`、`/install*` 和 `/api/admin/*` 只允许来自 `.env` 明确填写的最小 LAN/VLAN 前缀，其余请求返回 404。
5. Caddy 通过固定 Docker 网络地址连接 `backend:8881`；后端只信任该代理地址提供的转发头。
6. SQLite 数据与备份使用两个独立宿主目录，防止逻辑误操作相互覆盖；同一磁盘上的目录不提供物理灾备，正式灾备需要第二介质或离机复制。

## 端口与安全约束

- 容器内部后端端口：`8881`。
- 后端宿主端口：默认完全不发布；只有显式启用宿主机代理 override 时才绑定 `127.0.0.1:8881`，且不得配置公网端口转发。
- Caddy 宿主机端口：默认 `8080/8443`，可用环境文件调整。
- 公网端口：只开放 TCP `80/443`；UDP `443` 可选。
- 如果发布 AAAA 记录，IPv6 路径必须也能在标准 `443` 到达 Caddy。无法做到时不发布 AAAA，避免部分客户端优先 IPv6 后失败。
- 管理入口依赖 HTTPS、私网来源校验和后端管理员 CIDR 三层限制；公网 API 不暴露管理路由。
- `KEKEIO_ADMIN_NETWORKS`、`KEKEIO_ADMIN_ALLOWED_CIDRS` 和 `KEKEIO_TRUSTED_PROXIES` 都是必填精确值，不使用任意私网回退。

## 容器持久化与健康检查

- 后端以 UID/GID `10001` 非 root 用户运行。使用 bind mount 前必须创建数据与备份目录并授权给 `10001:10001`。
- `/data` 保存 SQLite、secrets、安装码与恢复日志；`/backups` 保存自动和手工备份。
- `FULLPRO_BACKUP_DIRECTORY=/backups` 在启动时覆盖向导持久值；目录会先通过写入、`fsync` 和删除探测，不可写时后端拒绝启动。
- 镜像级 Docker `HEALTHCHECK` 使用 `/health/live`，用于判断进程是否存活并避免首次安装阶段长期显示 unhealthy。
- `/health/ready` 继续表示数据库、schema、安装状态和维护门禁，供外部监控或编排系统单独使用。
- Caddy 等待后端 liveness 通过后启动，但不等待 readiness；安装前公开 API 可以返回后端的未就绪状态，局域网安装入口仍可访问。

## GitHub Actions

- 项目构建 Node.js 统一为 24 LTS；Docker 管理台构建阶段使用 `node:24-alpine`。
- `checkout`、`setup-node`、`setup-go`、artifact 与 Docker Actions 全部固定到当前 Node.js 24 兼容大版本的完整提交 SHA，并保留版本注释。
- `main` 推送继续生成测试结果、发布归档和多架构 GHCR 镜像。
- `Create GitHub Release` 仍只在 `v*` 标签触发；普通 `workflow_dispatch` 或 `main` 推送显示 skipped 是预期行为。

## 错误处理与运维

- Caddy 对未列入白名单的公网路径统一返回 404，避免错误暴露安装和管理页面。
- 数据目录不可写时后端应启动失败；部署文档在启动前给出目录权限命令。
- `FULLPRO_ADDR` 与 `FULLPRO_HEALTHCHECK_URL` 都在镜像中固定到 `8881`，避免单独覆盖导致漂移。
- 生产部署使用已发布的 `v*`、`sha-<完整提交SHA>` 标签或镜像 digest；升级前记录当前 digest 并创建完整备份。
- 默认只支持 Cloudflare“仅 DNS”；橙云需要单独维护 Cloudflare 可信代理并为 LAN 提供绕过代理的 split DNS。
- 路由器不支持 NAT loopback 时，可用 split DNS，但域名所指目标仍必须在标准 `80/443` 接收请求或继续执行到 `8080/8443` 的 LAN 侧 DNAT。

## 验证策略

按用户要求不运行本地应用测试或本地容器测试。本次只执行：

- YAML、Compose、Caddyfile 和 Dockerfile 的静态检查（工具可用时）；
- 全仓库端口与旧 Action 引用扫描；
- Git diff 与工作树边界检查；
- 由 GitHub Actions 在推送后执行正式测试、构建和多架构镜像发布。
