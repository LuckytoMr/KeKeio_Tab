# KeKeIO Tab

KeKeIO Tab 是一个本地优先的新标签页扩展，配套单机自托管同步后端、管理员工作台、发布管理、审计和加密备份恢复能力。

## 加载浏览器扩展

不要点击 Chrome 页面的“加载未打包的扩展程序”后选择 ZIP 文件。

1. 打开 `chrome://extensions`，并保持“开发者模式”开启。
2. 点击“加载未打包的扩展程序”。
3. 选择本项目的 `extension/dist` 目录；如果使用发布 ZIP，先解压 `kekeio-tab-extension.zip`，再选择解压后的目录。
4. 记录扩展卡片显示的 ID。正式安装后端时，将 `chrome-extension://<扩展ID>` 填入安装向导的允许来源；本地快捷模式会自动放行格式正确的 Chrome 扩展 ID。

## Docker 正式部署

后端只支持 Docker 正式部署。仓库已经提供路由器可直接使用的 Compose、Caddyfile 和环境样例：

- [路由器 Docker 部署指南](backend/deploy/router/README.md)
- [Compose 配置](backend/deploy/router/compose.yaml)
- [Caddy 路由策略](backend/deploy/router/Caddyfile)

默认网络链路：

```text
https://tab.kekeio.com:443
  -> 路由器 WAN 443
  -> Docker 主机 8443
  -> Caddy 容器 443
  -> backend 容器 9009
```

外部仍是标准 `80/443`，因此访问 `tab.kekeio.com` 不需要添加端口；外网根地址返回后端 liveness 状态，允许的局域网来源会跳转到后台。路由器可以把外部 `80/443` 转到 Docker 主机的 `8080/8443`；`9009` 只是后端内部/上游端口，绝不能直接做 WAN 转发。DNS 只负责域名解析，不负责端口转换。

快速部署入口：

```sh
cd backend/deploy/router
cp router.env.example .env
# 先填写 KEKEIO_IMAGE，再编辑数据目录、LAN CIDR 和 Docker 网段
docker compose --env-file .env -f compose.yaml pull
docker compose --env-file .env -f compose.yaml up -d
```

首次部署前还必须给数据与备份目录设置 UID/GID `10001` 写权限，并完成 DDNS、端口转发和 HTTPS 证书链路；完整顺序见部署指南。

## GitHub 自动构建与发布

`.github/workflows/publish.yml` 使用 Node.js 24 LTS，并在每次推送到 `main` 时运行后端、管理端和扩展验证，然后发布私有 `linux/amd64`、`linux/arm64` 镜像到 GHCR。正式部署选择工作流生成的不可变版本或完整提交标签：

```text
ghcr.io/<GitHub用户名>/kekeio-tab:sha-<完整提交SHA>
```

普通 `main` 推送或未选择 `v*` 标签的 `workflow_dispatch` 只做验证和镜像发布，不创建 Actions Artifact。推送 `v*` 标签时，GitHub Actions 会在云端构建发布包，并在同一个 Job 中直接上传到 GitHub Release：`kekeio-tab-backend.zip`、`kekeio-tab-extension.zip`、`kekeio-tab-docker-arm64.tar`、`kekeio-tab-router-arm64.tar` 及各自的 `.sha256` 校验文件。后端 ZIP 包含路由器部署文件；重跑同一个标签会安全覆盖同名 Release 资源。

```sh
git tag v0.1.0
git push origin v0.1.0
```

### 路由器 ARM64 离线镜像

从 `v*` GitHub Release 下载 `kekeio-tab-router-arm64.tar` 后，可直接导入完整的路由器离线镜像包。`kekeio-tab-docker-arm64.tar` 是仅含应用镜像的兼容包。两者都与 `bin/fullpro-server-linux-arm64` 不同：tar 是 Docker image archive，裸二进制不能用于 `docker load`。

```sh
docker load -i kekeio-tab-router-arm64.tar
docker image inspect kekeio-tab:arm64
```

导入后，完整安装和后台必须继续使用路由器部署包中的 Compose+Caddy：在 `.env` 设置 `KEKEIO_IMAGE=kekeio-tab:arm64`，然后执行 `docker compose --env-file .env -f compose.yaml up -d --pull never`。不要登录 GHCR，也不要执行 `docker compose pull`；Compose 会自行创建所需 bridge 网络，并继续使用环境文件中精确的 Caddy trusted-proxy 配置。完全离线时，`caddy:2.11.4-alpine` 也必须已存在于 Docker 本地镜像缓存。裸 bridge 启动示例只用于健康检查或已有 HTTPS 宿主代理，不能作为安装或后台入口；完整命令见路由器部署指南。

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
