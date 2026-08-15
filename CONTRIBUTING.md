# 贡献指南

感谢你关注 kekeio。项目欢迎可复现的 Bug 报告、文档修正、测试补充和范围清晰的功能改进。

## 开始之前

1. 搜索现有 Issue，确认问题没有被重复报告。
2. 对较大的行为变化先创建功能建议，说明使用场景、数据边界和兼容性影响。
3. 安全漏洞不要发布为普通 Issue，请使用 [安全政策](SECURITY.md) 中的私下报告方式。
4. 不要提交密码、Cookie、Token、私钥、真实数据库、备份、邮箱地址或包含私人信息的截图。

## 本地环境

- Go 版本以 [`backend/go.mod`](backend/go.mod) 为准。
- Node.js 使用仓库 CI 中声明的主版本。
- pnpm 版本以 `packageManager` 字段和锁文件为准。

安装前端依赖：

```powershell
pnpm --dir extension install --frozen-lockfile
pnpm --dir backend/admin-ui install --frozen-lockfile
```

## 修改原则

- 保持扩展零网站访问权限，不要新增主机权限或运行时动态授权。
- 保持本机数据、共享配置、凭据和运行状态之间的边界。
- 不要削弱安装会话、CSRF、来源校验、局域网限制或公网管理入口隐藏策略。
- 不要改变固定路由器路径、LAN 绑定、双容器网络模式和 Release 资产清单，除非项目所有者在对应 Issue 中明确批准。
- 用户可见文案默认使用简体中文；代码标识符、协议和 API 名称保留原文。

## 验证

提交 Pull Request 前运行：

```powershell
.\scripts\verify-all.ps1
```

发布工作流改动还需要通过 YAML 解析和 `actionlint`。如果受环境限制无法完成某项验证，请在 PR 中写明未验证项及原因，不要把局部测试描述为完整通过。

## Pull Request

- 一个 PR 聚焦一个主题，避免混入无关格式化或重构。
- 说明问题、解决方式、用户可见影响和回滚方式。
- 新行为应补充对应测试；安全边界变化必须同步更新安全基线。
- UI 变化请提供脱敏后的前后对比图，并检查键盘操作、窄窗口和高缩放场景。
- 保持提交信息简洁、可追踪。
