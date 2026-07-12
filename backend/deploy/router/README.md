# 路由器 Docker 部署

该目录是 KeKeIO Tab 后端的正式部署入口。默认拓扑：

```text
https://tab.kekeio.com:443
  -> 路由器公网 TCP 443
  -> Docker 主机 TCP 8443
  -> Caddy 容器 TCP 443
  -> backend 容器 TCP 9009
```

客户端始终访问 `https://tab.kekeio.com`，不会看到 `8443` 或 `9009`。外网访问根地址会返回后端 liveness 状态，允许的局域网来源会跳转到后台。`9009` 只属于后端和反向代理之间的上游链路，不得设置 WAN 端口转发。

## 前置条件

- 路由器或 Docker 主机架构为 `linux/amd64` 或 `linux/arm64`，并使用 Docker Compose v2；ARMv7、MIPS 镜像不在发布范围内。
- 数据目录使用本地 Linux 文件系统（优先 ext4）。不要把 SQLite 数据放在 FAT/exFAT、权限映射不完整的 SMB/NFS 或不可靠的网络盘上。
- `tab.kekeio.com` 的 A 记录指向路由器公网 IPv4。只有 IPv6 标准端口 `80/443` 也能到达 Caddy 时才发布 AAAA。
- 私有 GHCR 镜像需要一个具有 `read:packages` 权限的 GitHub PAT。

## 1. 准备环境文件和目录

```sh
# Git 仓库中：cd backend/deploy/router
# 后端 Release ZIP 解压后：cd deploy/router
cp router.env.example .env
```

编辑 `.env`，在线 GHCR 路径把 `KEKEIO_IMAGE` 改成 Actions 已发布的 `v*` 或 `sha-<完整提交SHA>` 标签；离线 ARM64 路径在加载 tar 后设置为 `KEKEIO_IMAGE=kekeio-tab:arm64`。同时确认数据路径、真实 LAN CIDR 和 Docker `/29` 网段。正式扩展把服务地址固定为 `https://tab.kekeio.com`，因此 `KEKEIO_DOMAIN` 必须保持 `tab.kekeio.com`；只有同时修改并重建扩展的 fork 才能使用其他域名。不要把浮动 `latest` 作为常规生产版本。然后创建两个逻辑独立的持久目录；真正抵御磁盘损坏时，应把 `KEKEIO_BACKUP_DIR` 放到第二块介质，或定期复制到另一台设备/离机存储：

```sh
mkdir -p /mnt/usb-24aeefbb/mi_docker/tab/data
mkdir -p /mnt/usb-24aeefbb/mi_docker/tab/backups
chown -R 10001:10001 /mnt/usb-24aeefbb/mi_docker/tab/data
chown -R 10001:10001 /mnt/usb-24aeefbb/mi_docker/tab/backups
chmod 700 /mnt/usb-24aeefbb/mi_docker/tab/data
chmod 700 /mnt/usb-24aeefbb/mi_docker/tab/backups
```

镜像内后端以 UID/GID `10001` 运行。bind mount 会覆盖镜像目录本身的属主，跳过上述授权会导致数据库、secrets 或安装码创建失败。Compose 禁止自动创建缺失的宿主目录；后端还会在启动时对 `/backups` 执行写入、`fsync`、删除探测，失败会直接退出，避免“服务 healthy 但备份持续失败”。

## 2. 加载离线 ARM64 镜像并启动完整 Compose+Caddy 拓扑

从 Actions 的 `kekeio-tab-release` 下载并解压 `kekeio-tab-docker-arm64.tar`，复制到路由器后执行：

```sh
docker load -i kekeio-tab-docker-arm64.tar
docker image inspect kekeio-tab:arm64
```

在 `.env` 中设置：

```text
KEKEIO_IMAGE=kekeio-tab:arm64
```

然后运行：

```sh
docker compose --env-file .env -f compose.yaml up -d --pull never
docker compose --env-file .env -f compose.yaml logs backend
```

离线 tar 不需要登录私有 GHCR，也不要执行 `docker compose pull`。`--pull never` 让镜像未在本地时直接失败而非访问网络；因此完全离线时 `caddy:2.11.4-alpine` 也必须已经在本地镜像缓存中。Compose 会自行创建 `kekeio-tab-edge` bridge 网络，无需手工 `docker network create`，并会继续使用 `.env` 中精确的 `KEKEIO_TRUSTED_PROXIES=172.30.88.2/32`（或随自定义静态 Caddy IP 相应收窄的 `/32`）。`9009` 仍只是后端 HTTP 上游端口，安装页、后台和正式扩展必须经过该 Compose+Caddy 的可信 HTTPS 入口；不要把 WAN `9009` 直接暴露到公网。

### 可选：SimpleDocker bridge 健康检查/已有 HTTPS 宿主代理上游

下面的裸 bridge 容器只适合访问 `/health/live`，或由已经具备 HTTPS、路径白名单和精确 trusted-proxy 配置的宿主代理作为受限上游使用。它不是 `/install` 或 `/admin` 的部署入口；首次安装和后台始终使用上面的 Compose+Caddy。将 `192.168.50.1` 替换为当前 Docker 主机的 LAN IP：

```sh
docker rm -f kekeio-tab-health 2>/dev/null || true
docker run -d \
  --name kekeio-tab-health \
  --network bridge \
  --restart unless-stopped \
  -p 192.168.50.1:9009:9009 \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  --pids-limit 128 \
  -e 'FULLPRO_ADMIN_ALLOWED_CIDRS=127.0.0.1/32,::1/128,192.168.50.0/24' \
  -v /mnt/usb-24aeefbb/mi_docker/tab/data:/data:rw \
  -v /mnt/usb-24aeefbb/mi_docker/tab/backups:/backups:rw \
  kekeio-tab:arm64
```

不得改成全接口 `-p 9009:9009`。裸 bridge 容器没有 Compose 中固定的 Caddy 地址，不能臆造 `FULLPRO_TRUSTED_PROXIES`；已有 HTTPS 宿主代理若要提供安装或后台，必须使用第 6 节的 `compose.host-proxy.yaml`，并把 `.env` 中的可信代理收窄为实际 bridge gateway 的精确 `/32`。

## 3. 私有 GHCR 与完整 Compose 部署

```sh
echo "$GHCR_TOKEN" | docker login ghcr.io -u LuckytoMr --password-stdin
docker compose --env-file .env -f compose.yaml pull
docker compose --env-file .env -f compose.yaml up -d
docker compose --env-file .env -f compose.yaml logs backend
```

首次启动日志会打印一次性安装码，并把它保存到数据目录的 `install-code`。Docker 健康检查使用 `/health/live`；Caddy 会等待该检查通过后启动。安装完成前 `/health/ready` 返回 503 是正常的 readiness 门禁。

## 4. 路由器端口与 DNS

如果 Docker 运行在一台独立 LAN 主机（例如 `192.168.50.9`），按下表添加 IPv4 TCP 转发：

| 名称 | 协议 | 外部端口 | 内部 IP | 内部端口 |
|---|---|---:|---|---:|
| KeKeIO HTTP | TCP | 80 | Docker 主机 LAN IP | 8080 |
| KeKeIO HTTPS | TCP | 443 | Docker 主机 LAN IP | 8443 |
| KeKeIO HTTP/3（可选） | UDP | 443 | Docker 主机 LAN IP | 8443 |

外部端口仍然是标准 `80/443`，所以域名不需要端口；`8080/8443` 只是路由器转发后的宿主机端口。

如果 Docker 就运行在路由器本机：

- 宿主机 `80/443` 未占用时，可把 `.env` 中的 `KEKEIO_HTTP_PORT`/`KEKEIO_HTTPS_PORT` 直接改成 `80/443`，只在 WAN 防火墙放行对应端口。
- 宿主机 `80/443` 已被路由器管理页面占用时，保留 `8080/8443`，使用路由器的“本机端口重定向/DNAT”把 WAN `80/443` 重定向到本机 `8080/8443`。部分家用路由器的“转发到内网设备”页面不能把目标设为路由器自身，此时需要系统防火墙规则，而不是普通客户端端口转发。

DNS 只负责把域名解析成 IP，不负责端口转换。本部署只支持 Cloudflare“仅 DNS”；不要直接启用橙云代理，否则 Caddy 看到的是 Cloudflare 边缘地址，LAN 管理匹配、客户端审计和限流都会失真。若未来需要橙云，必须另外维护 Cloudflare trusted proxies，并为 LAN 配置绕过 Cloudflare 的直连 split DNS。

IPv6 通常不做 NAT。AAAA 记录启用前必须确认外部 TCP `443` 能以标准端口直达 Caddy；只开放 `8443` 却发布 AAAA 会让部分客户端优先 IPv6 后连接失败。

## 5. 完成安装

局域网直接打开 `https://tab.kekeio.com` 会跳转到后台；首次安装也可直接打开：

```text
https://tab.kekeio.com/install
```

如果 LAN 不能回流公网地址，可使用 split DNS，但必须同时解决标准端口：

- 域名指向独立 Docker 主机时，把 `.env` 的 `KEKEIO_HTTP_PORT`/`KEKEIO_HTTPS_PORT` 改为 `80/443`，确保该主机端口未被占用。
- 保留主机 `8080/8443` 时，域名应指向具有 LAN 侧 `80/443 → Docker 主机 8080/8443` DNAT/redirect 的路由器地址。

仅把域名解析到监听 `8443` 的主机不会自动把浏览器的标准 `443` 转过去。不要修改扩展中的正式域名。

安装向导中：

- 公网 URL 填 `https://tab.kekeio.com`，不要追加 `:9009`、`:8443`。
- 允许来源填精确的 `chrome-extension://<扩展ID>`。
- 自动备份目录填 `/backups`，让备份与 `/data` 分离。

安装与管理路由只允许 `.env` 中 `KEKEIO_ADMIN_NETWORKS` 的来源通过 Caddy；后端还会再次校验 `KEKEIO_ADMIN_ALLOWED_CIDRS` 和 HTTPS 转发头。部署完成后必须从真正的外网验证以下地址返回 404：

```text
https://tab.kekeio.com/admin
https://tab.kekeio.com/install
https://tab.kekeio.com/api/admin/v1/overview
```

公网会放行根路径 liveness、插件 API、账号验证/重置页、账号页静态资源和健康端点。`KEKEIO_ADMIN_NETWORKS` 与 `KEKEIO_ADMIN_ALLOWED_CIDRS` 必须填写真实、最小的 LAN/VLAN 前缀，不能使用任意私网回退。若所在 Docker/路由器实现把 WAN 来源改写成允许的 LAN 地址，Caddy 与后端都无法可靠区分 WAN/LAN；必须在路由器防火墙隔离管理面，或改用只监听 LAN/VPN 的独立管理入口。

上面的真实外网 404 验证是上线阻断门禁：任一管理地址不是 404，就停止部署，不得把域名交给正式扩展使用。

## 6. 已有反向代理

默认 Compose 不发布后端宿主端口，避免绕过 Caddy 白名单。若现有代理也运行在容器中，优先把它加入 `kekeio-tab-edge` 网络，给它固定 IP，把 `.env` 的 `KEKEIO_TRUSTED_PROXIES` 改成该 IP 的 `/32`，然后只启动后端：

```sh
docker compose --env-file .env -f compose.yaml up -d backend
```

容器代理上游使用 `backend:9009`。若必须使用宿主机代理，先用 `docker network inspect kekeio-tab-edge` 确认实际 bridge gateway，把 `KEKEIO_TRUSTED_PROXIES` 改成该 gateway 的精确 `/32`，再显式启用端口 override：

```sh
docker compose --env-file .env -f compose.yaml -f compose.host-proxy.yaml up -d backend
```

宿主机代理上游使用 `127.0.0.1:9009`。Docker Engine 低于 28 时，同一二层网络的其他主机可能访问仅绑定 localhost 的发布端口；旧引擎不要使用这个 override，应改用同网络容器代理或额外防火墙隔离。代理必须清除客户端伪造的转发头，再设置可信的 `X-Forwarded-For`、`X-Forwarded-Proto`，并复制本目录 Caddyfile 的公网路径白名单（包括 `/account/assets/*`）。

## 7. 升级、回滚与备份

### 在线 GHCR 路径

生产更新优先使用版本标签或镜像 digest，不要长期依赖浮动的 `latest`：

```sh
docker image inspect "$(docker inspect kekeio-tab-backend --format '{{.Image}}')" --format '{{json .RepoDigests}}'
docker image inspect "$(docker inspect kekeio-tab-caddy --format '{{.Image}}')" --format '{{json .RepoDigests}}'
docker compose --env-file .env -f compose.yaml pull
docker compose --env-file .env -f compose.yaml up -d
```

升级前在后台创建完整备份并保存恢复口令，同时记录后端和 Caddy 两个镜像 digest。若升级未改变数据库 schema，可把 `.env` 的两个镜像都改回旧标签/digest 后重新创建容器；若新版本已经迁移数据库，必须先恢复与旧版本兼容的迁移前快照或完整备份，旧二进制会拒绝未来 schema。

### 离线 ARM64 tar 路径

升级前在后台创建完整备份并保留当前与上一版 `kekeio-tab-docker-arm64.tar`，不要用新 tar 覆盖旧 tar。加载新 tar 后，将 `.env` 中的 `KEKEIO_IMAGE` 保持/更新为本地固定标签 `kekeio-tab:arm64`，再由同一份 Compose+Caddy 拓扑重建后端：

```sh
docker load -i /path/to/kekeio-tab-docker-arm64-new.tar
# 编辑 .env：KEKEIO_IMAGE=kekeio-tab:arm64
docker compose --env-file .env -f compose.yaml up -d --pull never
```

回滚时加载保留的旧 tar，再次把 `.env` 中的 `KEKEIO_IMAGE` 设为 `kekeio-tab:arm64` 后执行相同命令：

```sh
docker load -i /path/to/kekeio-tab-docker-arm64-previous.tar
# 编辑 .env：KEKEIO_IMAGE=kekeio-tab:arm64
docker compose --env-file .env -f compose.yaml up -d --pull never
```

离线升级和回滚不登录 GHCR，也不执行 pull；裸 bridge 容器只用于健康检查或已有 HTTPS 代理的受限上游，不能为它编造安装后台升级流程。不要同时运行两个后端实例访问同一个 SQLite 数据目录。同一物理盘上的 `/data` 与 `/backups` 只提供逻辑隔离，不能替代第二介质或离机备份。
