# kekeio

[![验证与发布](https://github.com/LuckytoMr/KeKeio_Tab/actions/workflows/publish.yml/badge.svg?branch=main)](https://github.com/LuckytoMr/KeKeio_Tab/actions/workflows/publish.yml)
[![最新版本](https://img.shields.io/github/v/release/LuckytoMr/KeKeio_Tab?display_name=tag&sort=semver)](https://github.com/LuckytoMr/KeKeio_Tab/releases/latest)
[![main 最新构建](https://img.shields.io/badge/下载-main--latest-0969da)](https://github.com/LuckytoMr/KeKeio_Tab/releases/tag/main-latest)

kekeio 是一个本地优先、可自托管的新标签页项目：浏览器扩展负责搜索、快捷方式、分组和壁纸体验；可选的 Go 后端负责账号、配置同步、备份与管理。个人用户可以只使用扩展，也可以在私人 NAS 或 ARM64 路由器上用 Docker 部署后端，并通过 HTTPS 域名让自己的设备在外网访问同步服务。

<p align="center">
  <img src="docs/images/kekeio-tab-preview.webp" alt="kekeio 新标签页效果图：自定义壁纸、搜索框、快捷方式和侧边工具栏" width="100%">
</p>

<p align="center"><sub>实际新标签页效果；壁纸、网站图标与第三方商标归各自权利人所有。</sub></p>

## 为什么做这个项目

常见新标签页产品要么依赖厂商云服务，要么需要广泛的网站访问权限。kekeio 选择另一条路径：页面数据优先保存在当前浏览器，用户自行决定是否启用自托管同步；扩展构建产物保持零网站访问权限，后端也只暴露明确的 API 与账号页面。

## 核心能力

- 自定义分组、快捷方式、搜索引擎、布局和侧边工具栏。
- 内置、远程和本地壁纸，支持轮换、遮罩与模糊效果。
- 本地优先配置、版本化导入导出，以及冲突时显式选择本机或云端版本。
- 单机自托管账号与配置同步，使用 Go、SQLite 和独立管理工作台。
- 适合私人 NAS、Docker 主机和 ARM64 路由器的部署路径。
- 通过 Cloudflare Tunnel 提供 HTTPS 同步服务，无需把后端端口转发到 WAN。
- 每次推送到 `main` 都执行测试、安全基线与构建，并覆盖 `main-latest` Release。

## 使用方式

| 场景 | 需要什么 | 数据与访问方式 |
| --- | --- | --- |
| 只在当前浏览器使用 | 浏览器扩展 | 配置与本机壁纸留在浏览器中，不需要后端 |
| 多浏览器或多设备同步 | 扩展 + 自托管后端 | 在个人 NAS、Docker 主机或路由器中保存账号与允许同步的配置 |
| 外网访问同步服务 | 后端 + Cloudflare Tunnel | 扩展通过 `https://tab.kekeio.com` 访问；Tunnel 只建立出站连接 |

本机图片、图标 Blob、第三方凭据和设备运行状态不会作为共享配置上传。当前同步不是浏览器厂商账号同步的替代品，详细边界见 [产品事实](PRODUCT.md)。

## 下载浏览器扩展

每次 `main` 验证通过后都会自动更新以下固定下载：

- [下载浏览器扩展 ZIP](https://github.com/LuckytoMr/KeKeio_Tab/releases/download/main-latest/kekeio-tab-extension.zip)
- [下载 ARM64 Docker 离线包](https://github.com/LuckytoMr/KeKeio_Tab/releases/download/main-latest/kekeio-tab-docker-arm64.tar)

加载扩展：

1. 解压 `kekeio-tab-extension.zip`。
2. 打开 `chrome://extensions` 或 `edge://extensions`，启用开发者模式。
3. 点击“加载已解压的扩展程序”，选择解压后的 `dist` 目录。
4. 如需自托管同步，记录扩展 ID，并在后端安装向导中填入 `chrome-extension://<扩展ID>`。

不要直接选择 ZIP 文件，也不要从不明来源安装重新打包的版本。

## Docker 自托管

正式后端统一使用 Docker。当前固定 ARM64 路由器部署从 Release 下载 `kekeio-tab-docker-arm64.tar`；它同时包含 `kekeio-tab:arm64` 和构建时最新的 ARM64 `cloudflare/cloudflared:latest`：

```sh
docker load -i kekeio-tab-docker-arm64.tar
# 然后按 docker命令.txt 启动 kekeio-tab 与 cloudflared 两个容器
```

固定路由器环境中：

- LAN 为 `192.168.50.1/24`，应用只绑定 `192.168.50.1:9009`。
- 数据目录为 `/mnt/usb-24aeefbb/mi_docker/kekeio/data`。
- 备份目录为 `/mnt/usb-24aeefbb/mi_docker/kekeio/backups`。
- `cloudflared` 使用 `--network container:kekeio-tab`，源站为 `http://localhost:9009`。
- 不要把 `9009` 转发到 WAN，也不要把真实 Tunnel Token 提交到 GitHub。

完整步骤见 [路由器 Docker 部署指南](backend/deploy/router/README.md) 和 [固定 Docker 命令](docker命令.txt)。高级 Compose、Caddy 与隔离配置保留在 [`backend/deploy/router`](backend/deploy/router) 中。

## 安全与隐私

- Manifest 不声明 `host_permissions` 或 `optional_host_permissions`，构建后的扩展没有网站读取权限。
- GitHub Gist Token 只允许发送给 `api.github.com`，不会发送给任意快捷方式站点。
- UHDpaper 通过登录后的受限后端代理访问，后端不是任意 URL 代理。
- 安装和管理入口只允许本机及明确配置的局域网网段；公网请求返回 `404`。
- 安装会话保留 HttpOnly/SameSite Cookie、CSRF、来源校验、过期和并发提交保护。
- 密钥、Cookie、Token、数据库和备份均由 `.gitignore` 排除。

如发现安全问题，请不要公开附带利用细节的 Issue，按照 [安全政策](SECURITY.md) 私下报告。

## 开发与验证

主要技术栈为 Preact、TypeScript、Go 和 SQLite。依赖与构建命令分别记录在 [扩展说明](extension/README.md) 和 [后端说明](backend/README.md)。完整验证入口：

```powershell
.\scripts\verify-all.ps1
```

它会运行 Go 测试与 `go vet`、管理端测试与生产构建、扩展测试与类型检查，以及跨模块安全和产品展示基线。

## 参与维护

- 提交问题前请阅读 [贡献指南](CONTRIBUTING.md)。
- Bug 报告应包含版本、复现步骤、期望行为和已脱敏日志。
- Pull Request 必须说明影响范围，并通过 `scripts/verify-all.ps1`。
- 真实密码、Cookie、Token、私钥、数据库或私人备份不得进入 Issue、PR、截图和提交历史。

## 发布

`.github/workflows/publish.yml` 在每次推送到 `main` 后验证并覆盖 `main-latest`，无需额外创建标签或手动运行工作流。Actions 只上传两个自定义 Release 资产：

```text
kekeio-tab-extension.zip
kekeio-tab-docker-arm64.tar
```

GitHub 自动生成的 Source code ZIP/TAR.GZ 不属于项目自定义资产。
