# KeKeIO Tab 固定产品约束

以下约束由项目所有者明确决定。除非用户在当前任务中明确要求修改，否则任何实现、重构、安全加固或文档更新都不得改变这些行为。

## 安装与管理员

- 安装入口只允许本机和 `FULLPRO_ADMIN_ALLOWED_CIDRS` 中的局域网地址访问；已安装状态必须继续返回 `404`。
- 首次安装和管理员重置不使用一次性安装码，不生成 `install-code` 文件，不读取 `FULLPRO_INSTALL_CODE*`，前端不得显示安装码输入步骤。
- 安装向导应在确认服务处于 `uninitialized` 或 `requires_admin_reset` 后自动建立安装会话。
- 无安装码不等于无会话保护：安装会话的 HttpOnly/SameSite Cookie、CSRF、来源校验、过期时间、并发提交保护和管理网段限制必须保留。
- 管理员密码最低长度固定为 **4 个 Unicode 字符**，精确定义为 4 个 Unicode code point；不得 trim 或 normalize 密码。Go 使用 `utf8.RuneCountInString`，前端使用 `Array.from(password).length`。前后端必须一致，不得擅自提高到 8、12 或其他长度。普通插件用户密码和完整备份恢复口令策略不受这一约束影响。

## 路由器部署

- 固定 LAN 为 `192.168.50.1/24`，应用端口只绑定 `192.168.50.1:9009`，不得建议把 `9009` 转发到 WAN。
- 数据目录固定为 `/mnt/usb-24aeefbb/mi_docker/kekeio/data`，备份目录固定为 `/mnt/usb-24aeefbb/mi_docker/kekeio/backups`。
- `cloudflared.env` 只保存在路由器本地并重复使用；不得提交、输出或要求用户在聊天中粘贴真实 Tunnel Token。
- 直启模式由 `kekeio-tab` 与 `cloudflared-tab` 两个容器组成，cloudflared 使用 `--network container:kekeio-tab`，Cloudflare 源站固定为 `http://localhost:9009`。

## GitHub 发布

- Actions 只上传两个自定义 Release 资产：`kekeio-tab-extension.zip` 和 `kekeio-tab-docker-arm64.tar`。
- Docker tar 必须同时包含 ARM64 的 `kekeio-tab:arm64` 与构建时最新的 `cloudflare/cloudflared:latest`。
- 不得恢复 Actions Artifact、GHCR 发布、后端 ZIP、SimpleDocker 外层 ZIP、完整路由器归档或外层 `.sha256` 附件。
- GitHub 自动生成的 Source code ZIP/TAR.GZ 不属于项目自定义资产。

## 改动验证

- 涉及上述约束时必须同步更新后端测试、管理端测试和 `scripts/verify-security-baseline.ps1`。
- 提交前运行 `scripts/verify-all.ps1`；发布工作流改动还必须通过 YAML 解析和 `actionlint`。
