# kekeio

kekeio 是一个本地优先的新标签页扩展，配套单机自托管同步后端、管理员工作台、发布管理、审计和加密备份恢复能力。

## 加载浏览器扩展

不要点击 Chrome 页面的“加载未打包的扩展程序”后选择 ZIP 文件。

1. 打开 `chrome://extensions`，并保持“开发者模式”开启。
2. 点击“加载未打包的扩展程序”。
3. 选择本项目的 `extension/dist` 目录；如果使用发布 ZIP，先解压 `kekeio-tab-extension.zip`，再选择解压后的目录。
4. 记录扩展卡片显示的 ID。正式安装后端时，将 `chrome-extension://<扩展ID>` 填入安装向导的允许来源；本地快捷模式会自动放行格式正确的 Chrome 扩展 ID。

## Docker 正式部署

后端只支持 Docker 正式部署。当前固定设备（LAN `192.168.50.1/24`）只需从 GitHub Release 下载 `kekeio-tab-docker-arm64.tar`，上传到已保存 `cloudflared.env` 的目录后运行：

```sh
docker load -i kekeio-tab-docker-arm64.tar
# 然后执行仓库 docker命令.txt 中的两个 docker run
```

这个 tar 同时包含 `kekeio-tab:arm64` 和 GitHub 构建时最新的 `cloudflare/cloudflared:latest`，路由器无需另外 pull。应用数据固定写入 `/mnt/usb-24aeefbb/mi_docker/kekeio/data`，备份固定写入同级 `backups`；镜像会先修正这两个专用挂载点的权限，再以 UID `10001` 运行后端。Token 只写入路由器本地 `cloudflared.env` 一次，重建时继续复用。Cloudflare Published application 固定填写 `http://localhost:9009`，不再填写 bridge 网关或 HTTP Host Header。

直接模式链路：

```text
https://tab.kekeio.com -> Cloudflare Tunnel -> cloudflared -> localhost:9009
局域网管理员 -> http://192.168.50.1:9009
```

`cloudflared` 与后端共享网络命名空间时，后端只信任 loopback 代理并依据 Cloudflare 的 `X-Forwarded-For` 判断真实客户端；公网 `/install`、`/admin` 和管理 API 返回 `404`。LAN 管理 HTTP 是该固定路由器直启模式的显式折中，不得对 WAN 转发 `9009`。

首次从允许的局域网打开 `/install` 会自动建立受 Cookie、CSRF、来源与过期时间保护的安装会话，不生成也不要求一次性安装码。管理员密码最低固定为 4 个 Unicode 字符；这是本项目所有者确定的局域网管理策略，普通插件用户密码规则不受影响。

详细说明：

- [路由器 Docker 部署指南](backend/deploy/router/README.md)
- [固定 Docker 命令](docker命令.txt)
- [Compose 配置](backend/deploy/router/compose.yaml)
- [Caddy 路由策略](backend/deploy/router/Caddyfile)

仓库仍保留 Caddy/Compose 高级隔离配置，但 GitHub 不再为它额外构建路由器归档。

Tunnel 只建立出站连接，不需要路由器开放或转发任何 WAN 端口。下面是高级 Compose 入口：

```sh
cd backend/deploy/router
cp router.env.example .env
# 先填写 KEKEIO_IMAGE，再编辑数据目录、LAN CIDR 和 Docker 网段
docker compose --env-file .env -f compose.yaml pull
docker compose --env-file .env -f compose.yaml up -d
```

当前双容器 Tunnel 模式和高级 Caddy Tunnel 模式都只建立出站连接，不需要 WAN 入站配置。若自行停用 Tunnel 并改成公网直连，才需要另行处理 DNS、证书、防火墙和端口转发；这不属于本文主路径。

## GitHub 自动构建与发布

`.github/workflows/publish.yml` 使用 Node.js 24 LTS，并在每次推送到 `main` 时运行后端、管理端和扩展验证。验证通过后只构建并上传两个自定义 GitHub Release 文件：

- [浏览器扩展 ZIP](https://github.com/LuckytoMr/KeKeio_Tab/releases/download/main-latest/kekeio-tab-extension.zip)
- [ARM64 Docker 镜像 tar](https://github.com/LuckytoMr/KeKeio_Tab/releases/download/main-latest/kekeio-tab-docker-arm64.tar)

`kekeio-tab-docker-arm64.tar` 同时包含 `kekeio-tab:arm64` 与构建时最新的 ARM64 `cloudflare/cloudflared:latest`。普通 `main` 推送覆盖 `main-latest` 预发布；推送 `v*` 标签则上传到对应正式 Release。工作流会清理同一 Release 中不在上述白名单里的旧自定义资产，不创建 Actions Artifact，也不再构建或推送 GHCR 镜像。

```sh
git tag v0.2.2
git push origin v0.2.2
```

GitHub Release 页面还会自动显示 `Source code (zip)` 与 `Source code (tar.gz)`；这是 GitHub 对每个标签自动提供的源码下载，不能删除，也不属于本项目上传的构建资产。

## 非生产本地模式

仓库仍保留 `go run ./cmd/fullpro-server dev`，仅用于开发调试，固定监听 `127.0.0.1:8787` 并使用隔离的 `.dev-data`。它不是 Docker 或正式部署入口，正式扩展仍只连接 `https://tab.kekeio.com`。

## 验证

```powershell
.\scripts\verify-all.ps1
```
