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

推送 `v*` 标签还会创建私有 GitHub Release，并附带后端与扩展 ZIP；后端 ZIP 包含上述路由器部署文件。普通 `main` 推送或未选择 `v*` 标签的 `workflow_dispatch` 中，`Create GitHub Release` 显示 skipped 是预期行为。

### 路由器 ARM64 离线镜像

Actions 的 `kekeio-tab-release` 产物包含可直接导入 Docker 的 `kekeio-tab-docker-arm64.tar`。它与 `bin/fullpro-server-linux-arm64` 不同：前者是完整 Docker image archive，后者只是裸可执行文件，不能用于 `docker load`。

```sh
docker load -i kekeio-tab-docker-arm64.tar
docker image inspect kekeio-tab:arm64
```

离线镜像不需要登录私有 GHCR。完整目录准备和 `docker run` 命令见路由器部署指南。

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
