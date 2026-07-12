# KeKeIO Tab

KeKeIO Tab 是一个本地优先的新标签页扩展，配套单机自托管同步后端、管理员工作台、发布管理、审计和加密备份恢复能力。

## 加载浏览器扩展

不要点击 Chrome 页面的“加载未打包的扩展程序”后选择 ZIP 文件。

1. 打开 `chrome://extensions`，并保持“开发者模式”开启。
2. 点击“加载未打包的扩展程序”。
3. 选择本项目的 `extension/dist` 目录；如果使用发布 ZIP，先解压 `kekeio-tab-extension.zip`，再选择解压后的目录。
4. 记录扩展卡片显示的 ID。正式安装后端时，将 `chrome-extension://<扩展ID>` 填入安装向导的允许来源；本地快捷模式会自动放行格式正确的 Chrome 扩展 ID。

## Windows 本地启动后端

快速测试不需要设置任何环境变量，也不需要完成安装向导。在 PowerShell 中执行：

```powershell
cd .\backend
go run ./cmd/fullpro-server dev
```

若已构建二进制，也可执行 `.\bin\fullpro-server.exe dev`。首次运行会在 `backend/.dev-data` 创建独立测试数据库；管理员账号为 `admin@local.test`、插件账号为 `user@local.test`，两者密码固定为 `2231`。打开 `http://127.0.0.1:8787/admin` 登录后台。正式扩展固定连接 `https://tab.kekeio.com`，不会读取或保存本机自定义地址；因此本地 `dev` 模式主要用于快速验证后台和 API，端到端扩展同步需等该域名的 HTTPS 反向代理就绪。

该模式固定监听 `127.0.0.1:8787`，不会使用生产启动与安装配置，也不会碰触 `backend/data`。它只用于本机调试，不能用于 Docker 或正式部署。

## GitHub 自动构建与 Docker 部署

`.github/workflows/publish.yml` 会在每次推送到 `main` 时运行后端、管理端和扩展测试，并发布私有多架构镜像到 GHCR：

```text
ghcr.io/<GitHub用户名>/kekeio-tab:latest
```

推送 `v*` 标签还会创建私有 GitHub Release，并附带后端与扩展 ZIP。

私有 GHCR 镜像部署前，使用拥有 `read:packages` 权限的 GitHub Personal Access Token 登录：

```bash
echo "$GHCR_TOKEN" | docker login ghcr.io -u <GitHub用户名> --password-stdin
docker pull ghcr.io/<GitHub用户名>/kekeio-tab:latest
docker run -d --name kekeio-tab --restart unless-stopped \
  -p 127.0.0.1:8787:8787 \
  -v /srv/kekeio-tab:/data \
  -e FULLPRO_COOKIE_SECURE=true \
  -e FULLPRO_PUBLIC_BASE_URL=https://tab.kekeio.com \
  ghcr.io/<GitHub用户名>/kekeio-tab:latest
```

路由器中的 `ddns-go` 应让 `tab.kekeio.com` 的 A 与 AAAA 记录都指向同一台 HTTPS 反向代理。DNS 记录本身不会开放服务：IPv4 仍需转发 TCP 443，IPv6 需在防火墙放行 TCP 443，并确保两条链路都能到达代理和获得有效证书。完整配置见后端文档。

Docker、反向代理、备份和本地恢复细节见 [后端文档](backend/README.md)。

## 验证

```powershell
.\scripts\verify-all.ps1
```
