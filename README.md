# KeKeIO Tab

KeKeIO Tab 是一个本地优先的新标签页扩展，配套单机自托管同步后端、管理员工作台、发布管理、审计和加密备份恢复能力。

## 加载浏览器扩展

不要点击 Chrome 页面的“加载未打包的扩展程序”后选择 ZIP 文件。

1. 打开 `chrome://extensions`，并保持“开发者模式”开启。
2. 点击“加载未打包的扩展程序”。
3. 选择本项目的 `extension/dist` 目录；如果使用发布 ZIP，先解压 `kekeio-tab-extension.zip`，再选择解压后的目录。
4. 记录扩展卡片显示的 ID。正式安装后端时，将 `chrome-extension://<扩展ID>` 填入安装向导的允许来源；本地快捷模式会自动放行格式正确的 Chrome 扩展 ID。

## Docker 正式部署

后端只支持 Docker 正式部署。小米/SimpleDocker 的默认入口是一键 ARM64 包，不要求手工准备 Compose、Caddyfile、网段或镜像：

```sh
tar -xzf kekeio-tab-router-arm64.tar.gz && sh kekeio-tab-router-arm64/install.sh
```

安装器会加载 GitHub Actions 已构建的应用、Caddy 和 cloudflared 镜像，自动识别 LAN 与 Docker bridge、创建持久目录和安全网络并启动容器。首次运行只会无回显地询问新的 Cloudflare Tunnel Token；完成后会打印 Cloudflare Published application 需要填写的 Service URL 与 HTTP Host Header。高级手工部署仍可查阅：

- [路由器 Docker 部署指南](backend/deploy/router/README.md)
- [Compose 配置](backend/deploy/router/compose.yaml)
- [Caddy 路由策略](backend/deploy/router/Caddyfile)

默认网络链路：

```text
https://tab.kekeio.com
  -> Cloudflare Tunnel
  -> cloudflared
  -> Caddy 公网白名单
  -> backend:9009

局域网管理电脑
  -> https://路由器LAN地址:8443
  -> Caddy LAN 管理入口
  -> backend:9009
```

Tunnel 只建立出站连接，不需要路由器开放或转发任何 WAN 端口。公网只允许插件 API、账号页面和健康端点；安装、后台和管理 API 仅能从局域网 `8443` 进入。后端 `9009` 永远不发布到宿主机或 WAN。

下面仅是高级手工/直连配置入口，不是小米路由器默认部署方式：

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

普通 `main` 推送或未选择 `v*` 标签的 `workflow_dispatch` 只做验证和镜像发布，不创建 Actions Artifact。推送 `v*` 标签时，GitHub Actions 会在云端构建发布包，并在同一个 Job 中直接上传到 GitHub Release：`kekeio-tab-backend.zip`、`kekeio-tab-extension.zip`、兼容用的 `kekeio-tab-docker-arm64.tar`、一键路由器包 `kekeio-tab-router-arm64.tar.gz` 及各自的 `.sha256` 校验文件。路由器包内含完整 ARM64 镜像、安装器和 Caddy 公网白名单配置；重跑同一个标签会安全覆盖同名 Release 资源。

```sh
git tag v0.1.0
git push origin v0.1.0
```

### 路由器 ARM64 一键离线包

从 `v*` GitHub Release 下载 `kekeio-tab-router-arm64.tar.gz`，上传到路由器后执行一条命令：

```sh
tar -xzf kekeio-tab-router-arm64.tar.gz && sh kekeio-tab-router-arm64/install.sh
```

需要额外校验下载文件时，再同时下载同名 `.sha256` 并先运行 `sha256sum -c kekeio-tab-router-arm64.tar.gz.sha256`；安装器无论如何都会校验包内的 `images.tar`。安装器不依赖 `docker compose`，会直接使用 Docker CLI 加载包内的 `kekeio-tab:arm64`、`caddy:2.11.4-alpine` 和固定版本 cloudflared，然后创建与原 Compose 等价的隔离拓扑。Token 只保存到本地只读文件，不进入命令、环境变量、镜像或 GitHub。`kekeio-tab-docker-arm64.tar` 仍是仅含应用镜像的兼容包，不能替代一键路由器包。

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
