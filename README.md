# KeKeIO Tab

KeKeIO Tab 是一个本地优先的新标签页扩展，配套单机自托管同步后端、管理员工作台、发布管理、审计和加密备份恢复能力。

## 加载浏览器扩展

不要点击 Chrome 页面的“加载未打包的扩展程序”后选择 ZIP 文件。

1. 打开 `chrome://extensions`，并保持“开发者模式”开启。
2. 点击“加载未打包的扩展程序”。
3. 选择本项目的 `extension/dist` 目录；如果使用发布 ZIP，先解压 `kekeio-tab-extension.zip`，再选择解压后的目录。
4. 记录扩展卡片显示的 ID。正式安装后端时，将 `chrome-extension://<扩展ID>` 填入安装向导的允许来源；本地快捷模式会自动放行格式正确的 Chrome 扩展 ID。

## Docker 正式部署

后端只支持 Docker 正式部署。当前固定设备（LAN `192.168.50.1/24`）推荐下载 `kekeio-tab-simpledocker-arm64.zip`，解压后只需配置一次本地 `cloudflared.env`，再按包内 `docker命令.txt` 直接运行：

```sh
docker load -i kekeio-tab-docker-arm64.tar
# 然后执行 docker命令.txt 中的两个 docker run
```

应用数据和备份使用 Docker 命名卷，不依赖 SimpleDocker 容器终端中的 `/data` 与宿主机路径一致。Token 只写入路由器本地 `cloudflared.env` 一次，容器重启或按相同命令重建时直接复用。Cloudflare Published application 固定填写 `http://localhost:9009`，不再填写 LAN CIDR、bridge 网关或 HTTP Host Header。

直接模式链路：

```text
https://tab.kekeio.com -> Cloudflare Tunnel -> cloudflared -> localhost:9009
局域网管理员 -> http://192.168.50.1:9009
```

`cloudflared` 与后端共享网络命名空间时，后端只信任 loopback 代理并依据 Cloudflare 的 `X-Forwarded-For` 判断真实客户端；公网 `/install`、`/admin` 和管理 API 返回 `404`。LAN 管理 HTTP 是该固定路由器直启模式的显式折中，不得对 WAN 转发 `9009`。

需要 LAN HTTPS、Caddy 公网路径白名单和 Token 文件挂载时，仍可使用完整隔离包：

```sh
tar -xzf kekeio-tab-router-arm64.tar.gz && sh kekeio-tab-router-arm64/install.sh
```

详细说明：

- [路由器 Docker 部署指南](backend/deploy/router/README.md)
- [Compose 配置](backend/deploy/router/compose.yaml)
- [Caddy 路由策略](backend/deploy/router/Caddyfile)

Tunnel 只建立出站连接，不需要路由器开放或转发任何 WAN 端口。下面是高级 Compose 入口：

```sh
cd backend/deploy/router
cp router.env.example .env
# 先填写 KEKEIO_IMAGE，再编辑数据目录、LAN CIDR 和 Docker 网段
docker compose --env-file .env -f compose.yaml pull
docker compose --env-file .env -f compose.yaml up -d
```

手工直连模式还必须处理数据权限、DNS、证书、防火墙和端口转发；Tunnel 一键模式不需要这些 WAN 入站配置。

## GitHub 自动构建与发布

`.github/workflows/publish.yml` 使用 Node.js 24 LTS，并在每次推送到 `main` 时运行后端、管理端和扩展验证，然后发布私有 `linux/amd64`、`linux/arm64` 镜像到 GHCR。正式部署选择工作流生成的不可变版本或完整提交标签：

```text
ghcr.io/<GitHub用户名>/kekeio-tab:sha-<完整提交SHA>
```

普通 `main` 推送或未选择 `v*` 标签的 `workflow_dispatch` 只做验证和镜像发布，不创建 Actions Artifact。推送 `v*` 标签时，GitHub Actions 会在云端构建发布包，并在同一个 Job 中直接上传到 GitHub Release：`kekeio-tab-backend.zip`、`kekeio-tab-extension.zip`、应用镜像 `kekeio-tab-docker-arm64.tar`、直接运行包 `kekeio-tab-simpledocker-arm64.zip`、完整隔离包 `kekeio-tab-router-arm64.tar.gz` 及各自的 `.sha256` 校验文件。重跑同一个标签会安全覆盖同名 Release 资源。

```sh
git tag v0.1.0
git push origin v0.1.0
```

### 路由器 ARM64 一键离线包

从 `v*` GitHub Release 下载 `kekeio-tab-router-arm64.tar.gz`，上传到路由器后执行一条命令：

```sh
tar -xzf kekeio-tab-router-arm64.tar.gz && sh kekeio-tab-router-arm64/install.sh
```

需要额外校验下载文件时，再同时下载同名 `.sha256` 并先运行 `sha256sum -c kekeio-tab-router-arm64.tar.gz.sha256`；安装器无论如何都会校验包内的 `images.tar`。安装器改用 Docker 命名卷保存 SQLite、备份、Caddyfile 与 Tunnel Token，避免从 SimpleDocker 容器终端调用 Docker 时发生宿主 bind mount 路径错位。

私有 GHCR 镜像部署前，使用拥有 `read:packages` 权限的 GitHub Personal Access Token 登录：

```sh
echo "$GHCR_TOKEN" | docker login ghcr.io -u <GitHub用户名> --password-stdin
docker pull ghcr.io/<GitHub用户名>/kekeio-tab:sha-<完整提交SHA>
```

生产更新使用工作流发布的 `v*` 或 `sha-<完整提交SHA>` 镜像标签；升级前创建完整备份并记录当前 digest，不要依赖浮动的 `latest`。

## 非生产本地模式

仓库仍保留 `go run ./cmd/fullpro-server dev`，仅用于开发调试，固定监听 `127.0.0.1:8787` 并使用隔离的 `.dev-data`。它不是 Docker 或正式部署入口，正式扩展仍只连接 `https://tab.kekeio.com`。

## 验证

```powershell
.\scripts\verify-all.ps1
```
