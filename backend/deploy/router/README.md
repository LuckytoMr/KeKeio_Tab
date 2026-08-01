# 小米万兆路由器 Docker 与 Cloudflare Tunnel 部署

本目录是 kekeio 后端的正式路由器部署入口。针对当前固定为 `192.168.50.1/24` 的小米万兆路由器，提供“两个容器直接运行”和“Caddy 完整隔离”两种受支持路径。

## 最简：Docker 直接运行

GitHub Actions 的成功摘要会直接链接到 `main-latest` 滚动预发布。部署时只下载 `kekeio-tab-docker-arm64.tar`；`docker命令.txt` 和 `cloudflared.env.example` 保留在仓库中，不再包装成额外 ZIP。

操作顺序：

1. 保留路由器现有的 `cloudflared.env`；首次部署才把示例复制为该文件并填写一次 Tunnel Token。
2. 执行 `docker load -i kekeio-tab-docker-arm64.tar`。归档同时载入后端与 GitHub 构建时最新的 ARM64 cloudflared，不需要另行 pull。
3. 原样执行 `docker命令.txt` 中的两个 `docker run`，顺序必须是应用在前、cloudflared 在后。
4. Cloudflare Published application 填 `HTTP`、`http://localhost:9009`，HTTP Host Header 留空。

该模式把应用数据固定到 `/mnt/usb-24aeefbb/mi_docker/kekeio/data`，备份固定到同级 `backups`。这些路径由宿主 Docker daemon 解释，不使用 SimpleDocker 终端内部的 `/data`。Docker 首次自动创建 bind 目录时通常属于 root；镜像入口只修正这两个挂载点，验证可写后立即降权为 UID `10001`，避免 SQLite `unable to open database file (14)`。LAN 安装入口为 `http://192.168.50.1:9009/install`；打开后会自动建立安装会话，不需要一次性安装码，管理员密码最低为 4 个 Unicode 字符。`9009` 只绑定这个 LAN 地址，严禁 WAN 转发。

`cloudflared` 通过 `--network container:kekeio-tab` 访问 loopback。这个用法只有在命令同时保留 `FULLPRO_TRUSTED_PROXIES=127.0.0.1/32`、精确管理 CIDR 和 LAN HTTP 显式开关时才受支持：Cloudflare 提供的 `X-Forwarded-For` 会用于恢复真实公网客户端 IP，公网管理路径因此返回 `404`，而不是被误认为 loopback 管理员。目标 SimpleDocker 会把 LAN 请求呈现为默认 bridge 网关，因此只把已确认的 `172.17.0.1/32` 加入管理 CIDR；它绝不能加入可信代理。

Token 保存在路由器本地 `cloudflared.env`，以后 `docker restart` 或按同一命令重建都不再交互输入。它会出现在本机 Docker 容器环境配置中，因此只有可信管理员可以访问 Docker API；绝不能提交真实文件到 GitHub。删除并重建应用容器时必须同时重建 cloudflared，因为 `container:` 绑定的是目标容器的网络命名空间。

## 完整隔离拓扑

需要 LAN HTTPS、Caddy 路径白名单、Token 只读文件和完全离线 cloudflared 时，使用以下拓扑：

```text
公网用户
  -> Cloudflare HTTPS
  -> cloudflared 独立容器
  -> Caddy :8081 公网白名单入口
  -> backend:9009

局域网管理员
  -> https://路由器LAN地址:8443
  -> Caddy 本地 CA HTTPS 管理入口
  -> backend:9009
```

公网入口只允许 `/`、`/api/v1/*`、账号验证/重置资源和健康端点。`/admin*`、`/install*`、`/api/admin/*` 永远不会进入 Tunnel。后端 `9009` 不发布到 WAN。

## 完整隔离：源码高级方案

仓库继续保留 Caddy、Compose 和 `install.sh` 供排错、审计或自行构建高级隔离环境，但 GitHub Actions 不再生成完整路由器归档、后端 ZIP 或外层校验附件。当前受支持的预构建部署主路径是文首的双容器直启模式。

## 必须先处理的安全事项

1. 示例 Token 可以写占位值；真实 Token 只能保存在路由器本地配置，不放进源码、Git、Release、聊天或截图。
2. 只有本页“Docker 直接运行”的完整参数允许 `--network container:kekeio-tab`；不要删掉可信代理、精确管理 CIDR 或 LAN 端口绑定，也不要套用到其他容器。
3. 完整隔离模式继续使用只读 Token 文件；直接模式按用户要求使用受限的 `cloudflared.env`，两者都不得提交真实凭据。
4. 不要把 SimpleDocker 管理页面、Docker socket、后端 `9009`、Tunnel metrics `20241` 暴露到 Tunnel 或 WAN。
5. 截图中的 Docker Engine `20.10.17` 已非常旧。只运行来源可信、固定版本、校验过摘要的镜像；不要在路由器上构建不可信 Dockerfile。优先等待小米固件或可信维护方提供引擎更新，不要用陈旧的 SimpleDocker 公共源码覆盖当前安装。

## 当前设备与 SimpleDocker 结论

截图确认设备为 `linux/arm64`、4 核、内核 `5.4.164`、Docker Engine `20.10.17`，Docker 数据目录位于 `/mnt/usb-24aeefbb/mi_docker/lib/docker`。架构与本项目 ARM64 镜像匹配。

公开的 [SimpleDocker 仓库](https://gitee.com/taoes_admin/SimpleDocker) 已标记关闭，公开 Release 最高只到 `0.0.7.1`；同一路由器历史部署记录使用的是 `0.0.7.2`。因此公开源码不是当前安装包的精确来源，不建议自行替换或“升级”。Docker 命令应在 SimpleDocker 的管理终端中运行，不是在受限的 OpenWrt 根 Shell 中运行。

## 部署文件

- `compose.yaml`：后端和 Caddy 的基础拓扑。
- `compose.tunnel.yaml`：标准 Docker 网络可正常出网时使用。
- `compose.tunnel-simpledocker.yaml`：小米/SimpleDocker 推荐兼容方案；`cloudflared` 使用内置 `bridge` 出网。
- `Caddyfile.tunnel`：Tunnel 公网白名单与 LAN 管理入口严格分离。
- `router.env.example`：不含凭据的环境样例。
- `install.sh`：不依赖 Compose 的一键安装与更新入口。

## 高级手工部署

### 1. 检查运行环境

在 SimpleDocker 管理终端执行：

```sh
uname -m
docker version
docker compose version
docker-compose version
docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}'
```

预期架构为 `aarch64` 或 `arm64`。记录最后一条命令输出的默认 bridge 网关，后面必须写入 `KEKEIO_TUNNEL_ORIGIN_BIND`，不要假定它一定是 `172.17.0.1`。

下文高级完整隔离路径优先使用 `docker compose`。如果只有 `docker-compose`，把后续命令中的 `docker compose` 替换成 `docker-compose`；两者都没有时可改用文首经过约束的 Docker 直接运行路径。

### 2. 准备 ARM64 离线镜像

Release 只提供一个 Docker 归档，可同时导入应用与构建时最新的 ARM64 cloudflared：

```sh
docker load -i kekeio-tab-docker-arm64.tar
```

该 tar 不包含 Caddy。若自行搭建高级隔离模式，需另外准备与本目录配置匹配的 Caddy 镜像；不要和文首双容器命令混用。

### 3. 准备目录和环境

从仓库源码中把 `backend/deploy/router` 目录复制到路由器，例如：

```text
/mnt/usb-24aeefbb/mi_docker/tab/deploy/router
```

进入该目录后：

```sh
cp router.env.example .env
mkdir -p /mnt/usb-24aeefbb/mi_docker/tab/data
mkdir -p /mnt/usb-24aeefbb/mi_docker/tab/backups
mkdir -p /mnt/usb-24aeefbb/mi_docker/tab/secrets
chown -R 10001:10001 /mnt/usb-24aeefbb/mi_docker/tab/data
chown -R 10001:10001 /mnt/usb-24aeefbb/mi_docker/tab/backups
chmod 700 /mnt/usb-24aeefbb/mi_docker/tab/data
chmod 700 /mnt/usb-24aeefbb/mi_docker/tab/backups
chmod 700 /mnt/usb-24aeefbb/mi_docker/tab/secrets
```

数据目录必须位于支持 SQLite 锁、WAL、`fsync`、原子 rename 和 Unix 权限的本地 Linux 文件系统，优先 ext4。不要使用 FAT/exFAT、权限映射不完整的 SMB/NFS 或不可靠网络盘。

编辑 `.env`，至少核对：

```text
KEKEIO_IMAGE=kekeio-tab:arm64
KEKEIO_ADMIN_HOST=<路由器真实LAN地址>
KEKEIO_HTTP_BIND=<路由器真实LAN地址>
KEKEIO_HTTPS_BIND=<路由器真实LAN地址>
KEKEIO_ADMIN_NETWORKS=<真实且最小的管理LAN/VLAN>
KEKEIO_ADMIN_ALLOWED_CIDRS=127.0.0.1/32,::1/128,<真实且最小的管理LAN/VLAN>
KEKEIO_TUNNEL_ORIGIN_HOST=origin-<前32个随机十六进制字符>.<后32个随机十六进制字符>.invalid
KEKEIO_TUNNEL_ORIGIN_BIND=<上一步查到的默认bridge网关>
```

在可信终端生成 256 位随机源站 Host，把输出完整填入 `.env` 中已有的 `KEKEIO_TUNNEL_ORIGIN_HOST=`，不要使用示例字面值：

```sh
origin_hex="$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')"
printf 'origin-%s.%s.invalid\n' \
  "$(printf '%s' "$origin_hex" | cut -c1-32)" \
  "$(printf '%s' "$origin_hex" | cut -c33-64)"
unset origin_hex
chmod 600 .env
```

该值不是 Cloudflare Token，但它是 Tunnel 到 Caddy 的共享源站鉴权值，只应保存在受限 `.env` 和 Cloudflare Published application 设置中，不要发布到仓库、聊天或截图。即使旧 Docker 意外让 `18081` 可被局域网或其他容器访问，请求也必须同时猜中该随机 Host 才会进入公网 API 白名单。

以下值应保持精确：

```text
KEKEIO_CADDY_IP=172.30.88.2
KEKEIO_BACKEND_IP=172.30.88.3
KEKEIO_TRUSTED_PROXIES=172.30.88.2/32
```

如果 `172.30.88.0/29` 与 LAN、VPN 或现有 Docker 网络重叠，整体换成一个不重叠的 `/29`，并同步修改三个静态地址。不要把 Docker 子网、`172.17.0.0/16` 或 cloudflared 地址加入管理网和可信代理。

### 4. 创建新的 Token 文件

先在 Cloudflare 控制台创建或重建 Tunnel，复制新 Token。不要执行控制台给出的含明文 Token 的 `docker run` 命令。

在 SimpleDocker 终端用无回显输入写入：

```sh
token_file=/mnt/usb-24aeefbb/mi_docker/tab/secrets/cloudflare-tunnel-token
umask 077
printf '粘贴新的 Tunnel Token（输入不可见），然后按回车：' >&2
trap 'stty echo' EXIT INT TERM
stty -echo
IFS= read -r tunnel_token
stty echo
trap - EXIT INT TERM
printf '\n' >&2
printf '%s' "$tunnel_token" > "$token_file"
unset tunnel_token
chown 65532:65532 "$token_file"
chmod 400 "$token_file"
stat -c '%u:%g %a %n' "$token_file"
```

预期权限为：

```text
65532:65532 400 .../cloudflare-tunnel-token
```

不要用 `cat`、`echo`、`docker inspect` 或日志回显 Token。若 USB 文件系统无法保存上述属主和权限，停止部署并先换用合适文件系统。

### 5. 选择 Tunnel 网络方案

### 小米/SimpleDocker 推荐方案

同一路由器历史项目发现自定义 Compose bridge 可能出现出网兼容问题，因此优先让 cloudflared 单独使用 Docker 内置 `bridge`，再通过只含公网白名单的 Caddy 专用端口访问应用：

```sh
docker compose -p kekeio-tab \
  --env-file .env \
  -f compose.yaml \
  -f compose.tunnel-simpledocker.yaml \
  config
```

确认配置能解析且输出中没有 Token 明文后启动：

```sh
docker compose -p kekeio-tab \
  --env-file .env \
  -f compose.yaml \
  -f compose.tunnel-simpledocker.yaml \
  up -d --pull never
```

若旧 Compose 不认识 `--pull never`，先逐一确认三个固定镜像都已存在，再删除该参数重试。不要改用 `latest`。

该方案把 Caddy 的 `8081` 只绑定到默认 bridge 网关，例如 `172.17.0.1:18081`。由于旧 Docker 的端口隔离较弱，仍必须从真实 LAN 实测 `/admin`、`/install` 和 `/api/admin/*` 均为 404；专用监听器本身也已硬编码为只有公网路径白名单。

### 标准 Docker 方案

如果以下联网测试确认 `kekeio-tab-edge` 能稳定出网，可避免宿主端口并让 cloudflared 直接加入该独立网络：

```sh
docker compose -p kekeio-tab \
  --env-file .env \
  -f compose.yaml \
  -f compose.tunnel.yaml \
  up -d --pull never
```

标准 Caddy 方案中 cloudflared 固定为 `172.30.88.4`，但后端仍只信任 Caddy 的 `172.30.88.2/32`。不要把文首直启模式的 loopback 信任参数混入该拓扑。

### 6. 配置 Cloudflare Published application

在 Cloudflare 控制台进入 `Networking -> Tunnels`，打开新 Tunnel，添加 Published application：

```text
Hostname: tab.kekeio.com
Type: HTTP
```

Service URL 按启动方案填写：

```text
小米/SimpleDocker： http://<KEKEIO_TUNNEL_ORIGIN_BIND>:<KEKEIO_TUNNEL_ORIGIN_PORT>
标准 Docker：      http://caddy:8081
```

例如 `.env` 已确认 bridge 网关为 `172.17.0.1` 时，小米方案填写：

```text
http://172.17.0.1:18081
```

在 `Additional application settings -> HTTP settings -> HTTP Host Header` 中填写 `.env` 的 `KEKEIO_TUNNEL_ORIGIN_HOST` 完整值。不要填公开域名，也不要留空；Cloudflare 会在 cloudflared 访问 Caddy 时覆盖 Host，Caddy 则拒绝缺少该随机值的直连请求。

完整 Caddy 模式不要填写 `backend:9009` 或 `localhost:9009`；只有文首两个容器直启模式固定填写 `http://localhost:9009`。任何模式都不得把路由器管理页面或 SimpleDocker 发布到 Tunnel。

Tunnel 会建立出站连接，不需要开放或转发 WAN `80/443/8080/8443/9009/18081/20241`。删除指向家庭公网 IP 的旧 A/AAAA 记录，避免绕过 Tunnel；让控制台为 Tunnel 管理对应代理 DNS。Cloudflare 边缘启用 HTTP 到 HTTPS 重定向。

不要给整个域名套一个会阻断扩展 API 的交互式 Cloudflare Access 登录页。若以后需要 Access，应使用独立管理域名并重新设计后端信任边界。

### 7. 首次安装与局域网 HTTPS

Tunnel 配置使用 `Caddyfile.tunnel`：

- 公网 `:8081` 是明文容器内链路，但只允许公网白名单。
- LAN 管理入口使用 Caddy 本地 CA，外部无法通过 Tunnel 到达。

导出本地 CA 的公开根证书：

```sh
mkdir -p /mnt/usb-24aeefbb/mi_docker/tab/certs
docker cp \
  kekeio-tab-caddy:/data/caddy/pki/authorities/local/root.crt \
  /mnt/usb-24aeefbb/mi_docker/tab/certs/kekeio-tab-local-root.crt
sha256sum /mnt/usb-24aeefbb/mi_docker/tab/certs/kekeio-tab-local-root.crt
```

只把 `root.crt` 安装到专用管理电脑的“受信任的根证书颁发机构”，不要复制或导出 Caddy 的 `root.key`。删除 `caddy-data` 卷会生成新的 CA，届时必须重新核对指纹并替换旧信任。

管理浏览器打开：

```text
https://<KEKEIO_ADMIN_HOST>:8443/install
```

若 `.env` 中 `KEKEIO_ADMIN_HOST=192.168.50.1`，地址就是：

```text
https://192.168.50.1:8443/install
```

从允许的局域网打开安装入口后，页面会自动建立受 Cookie、CSRF、来源与过期时间保护的安装会话，不生成或要求一次性安装码。

安装向导中：

- 公网 URL 填 `https://tab.kekeio.com`，不附加 `:9009`、`:8443` 或 `:18081`。
- 允许来源填精确的 `chrome-extension://<扩展ID>`。
- 备份目录填 `/backups`。
- 管理员密码至少填写 4 个 Unicode 字符。
- 必须完成 SMTP 测试；失败时先检查容器 DNS、TCP 出网和系统时间。

安装前 `/health/ready` 返回 503 是正常门禁；安装完成后必须变成 200。

### 8. 上线验收

### 容器与网络

```sh
docker ps --filter name=kekeio-tab --filter name=cloudflared-tab
docker inspect kekeio-tab-backend --format '{{json .NetworkSettings.Networks}}'
docker inspect kekeio-tab-caddy --format '{{json .NetworkSettings.Networks}}'
docker inspect cloudflared-tab --format '{{json .NetworkSettings.Networks}}'
docker logs --tail 100 cloudflared-tab
```

小米方案预期：

- backend：仅 `kekeio-tab-edge`。
- Caddy：仅 `kekeio-tab-edge`。
- cloudflared：仅内置 `bridge`。
- 三个容器最终均为 healthy，Cloudflare 控制台显示 Tunnel Healthy。

### Caddy 专用入口

在路由器本机测试，替换为真实 bridge 网关和端口：

```sh
origin_host="$(sed -n 's/^KEKEIO_TUNNEL_ORIGIN_HOST=//p' .env | tail -n 1)"
curl -i http://172.17.0.1:18081/health/live
curl -i -H "Host: ${origin_host}" http://172.17.0.1:18081/health/live
curl -i -H "Host: ${origin_host}" http://172.17.0.1:18081/admin
curl -i -H "Host: ${origin_host}" http://172.17.0.1:18081/install
curl -i -H "Host: ${origin_host}" http://172.17.0.1:18081/api/admin/v1/auth/session
unset origin_host
```

预期依次为 `404、200、404、404、404`。第一项验证缺少随机源站 Host 时连健康端点也被拒绝；后四项验证正确 Host 只能进入公网白名单，仍不能进入管理面。如果路由器没有 `curl`，可从另一个带 curl 的诊断容器测试；不要为了测试把 `18081` 改绑到 `0.0.0.0`。

再从真实 LAN 设备尝试 `http://<bridge网关>:18081/health/live` 和 `/admin`，两者都必须返回 404。即使旧 Docker/路由器把该地址路由到 LAN，未知随机 Host 的直连请求也不能进入公网 API；任一路径不是 404，立即停止上线。

### 公网

用手机关闭 Wi-Fi 后执行或访问：

```sh
curl -i https://tab.kekeio.com/health/live
curl -i https://tab.kekeio.com/health/ready
curl -i https://tab.kekeio.com/admin
curl -i https://tab.kekeio.com/install
curl -i https://tab.kekeio.com/api/admin/v1/auth/session
```

安装完成后的预期为 `200、200、404、404、404`。任一管理入口返回 200、302、401 或 403，都说明请求进入了不该进入的管理链路，必须下线调查。

最后检查：

```sh
docker inspect cloudflared-tab --format '{{json .Config.Env}}'
docker inspect cloudflared-tab --format '{{json .Config.Cmd}}'
```

输出中只能看到 Token 文件路径，不能出现 Token 本身。

### 9. 备份、升级与回滚

升级前：

1. 在后台创建一份完整备份并保存恢复口令。
2. 把备份复制到第二介质或离机存储。
3. 记录三个镜像的 ID 和 RepoDigest。
4. 保留当前与上一版 `kekeio-tab-docker-arm64.tar`。

```sh
docker image inspect kekeio-tab:arm64 --format '{{.Id}} {{json .RepoDigests}}'
docker image inspect caddy:2.11.4-alpine --format '{{.Id}} {{json .RepoDigests}}'
docker image inspect cloudflare/cloudflared:2026.7.3 --format '{{.Id}} {{json .RepoDigests}}'
```

双容器直启模式上传新 `kekeio-tab-docker-arm64.tar`，重新执行 `docker load`，然后按文首顺序重建应用与 cloudflared。高级手工 Compose 部署仍按原 `.env` 和两份 Compose 文件重建。

不要同时运行两个后端实例访问同一个 SQLite 数据目录。若新版本迁移了数据库，二进制回滚前必须恢复与旧版本兼容的迁移前快照或完整备份。

轮换 Tunnel Token 时：先在 Cloudflare 控制台 Rotate，再安全覆盖本地 Token 文件并重建 cloudflared；最后强制断开旧连接并验证新 connector Healthy。

### 10. 常见故障

### Compose 解析失败

先运行：

```sh
docker compose -p kekeio-tab --env-file .env -f compose.yaml -f compose.tunnel-simpledocker.yaml config
```

若只有旧 `docker-compose`，替换命令名并保留 `-p kekeio-tab`。旧版本如果不支持 `create_host_path: false`，不要直接删除安全检查；先确保数据、备份和 Token 路径都真实存在，再评估升级 Compose。

### cloudflared 无法连接

检查系统时间、DNS 和出站 TCP/UDP `7844`、HTTPS `443`。`cloudflared` 2026.5.2 之后会在启动时执行连接预检，日志会指出 DNS、QUIC/TCP 或 API 连通性问题。可临时把协议固定为 `http2` 诊断 UDP 7844 被阻断的情况，但解决防火墙后应恢复默认 `auto`。

### 公网返回 502

依次检查：

```sh
docker ps
docker logs --tail 100 kekeio-tab-backend
docker logs --tail 100 kekeio-tab-caddy
docker logs --tail 100 cloudflared-tab
```

确认 Cloudflare Service URL 与所选方案一致。绝不能把 502 的“修复”改成直连 `backend:9009`。

### 管理页面证书不受信

确认访问地址中的 IP 与 `KEKEIO_ADMIN_HOST` 完全一致，并重新核对、导入 `kekeio-tab-local-root.crt`。只信任公开根证书，绝不导出私钥。

### SMTP 测试失败

先从后端容器检查 DNS 和目标 SMTP 端口。若小米 Docker 的自定义 bridge 确实无法出网，不要把 backend 加入 cloudflared 的 namespace，也不要放宽可信代理；应修复 Docker NAT/防火墙，或使用 LAN 内可信 SMTP relay。该问题必须在正式开放注册前解决。

### 路由器资源不足

路由器只有 4 核且存储为 USB。保留较小的密码哈希并发，监控 SQLite、WAL、自动备份、访问日志和磁盘真实可用空间。上线前用接近真实大小的数据库执行备份并观察 `/health/ready` 与同步延迟。

### 11. 不使用 Tunnel 的直连模式

只有明确需要 WAN 端口转发时才使用基础 `Caddyfile` 和 `compose.yaml`：

```sh
docker compose -p kekeio-tab --env-file .env -f compose.yaml up -d
```

此模式要求域名 A/AAAA、标准 WAN `80/443`、Caddy 证书签发和路由器防火墙全部正确。不要同时保留一个能绕过 Tunnel 的 WAN 直连入口。完整 Tunnel 部署不需要任何 WAN 入站端口。

## 官方参考

- [Cloudflare Tunnel Token 权限、轮换与强制断开](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/remote-tunnel-permissions/)
- [cloudflared Tunnel run 参数与 token-file](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/run-parameters/)
- [Cloudflare Tunnel 防火墙要求](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-with-firewall/)
- [Cloudflare Tunnel 监控与 metrics](https://developers.cloudflare.com/tunnel/monitoring/)
- [Docker 端口发布与旧版本 localhost 风险](https://docs.docker.com/engine/network/port-publishing/)
- [Docker 默认 bridge 与 legacy links](https://docs.docker.com/engine/network/links/)
- [Caddy reverse_proxy 可信代理说明](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)
