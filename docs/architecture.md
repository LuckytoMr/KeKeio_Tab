# KeKeIO Tab 系统架构

日期：2026-07-12

状态：已实施，作为架构与兼容边界记录

目标版本：完成一次不保留已知高风险缺口的生产化升级

## 1. 背景与目标

KeKeIO Tab 是本地优先的新标签页扩展。后端运行在路由器或家用服务器上，插件 API 可通过 HTTPS 域名访问，管理后台只允许本机或局域网访问。

本次生产化升级前的实现已具备 Go + SQLite 后端、插件账号、配置快照、壁纸/风格元数据、版本记录和嵌入式后台，但仍存在以下缺口：

- 固定管理员口令 `lucky / 2231` 会在每次启动时重置。
- `/admin/assets/` 可绕过 `/admin` 的网络门读取后台 HTML；代理 IP 解析还可与固定口令组合成接管链。
- 多设备同步忽略 `baseVersion`，会静默覆盖远端数据；幂等记录与配置写入不在同一事务。
- 自动同步依赖新标签页内存定时器，页面关闭、后台挂起或频繁壁纸轮换都会使同步失效或长期饥饿。
- 后台是一个全量加载的超长 CRUD 单页，缺少同步诊断、资源发布生命周期、分区错误处理、可靠移动端和完整可访问性。
- 远程 CSS 未限制，后台失陷后可伪造扩展界面或触发外联请求。
- 官方壁纸、发布记录、配置历史等后端能力尚未形成完整的扩展端闭环。

本设计的成功标准：

1. 移除已确认的后台接管链，管理员和插件用户使用相互隔离的认证体系。
2. 实现可跨页面、浏览器重启和离线状态持续工作的多设备同步。
3. 非重叠修改自动合并；重叠修改绝不静默丢失，并提供人工解决界面。
4. 将后台重构为分区式运维工作台，能够完成安装、排障、用户支持、内容发布、安全配置和备份恢复。
5. 开放注册必须包含邮箱验证、密码找回、限流、配额和封禁能力。
6. 官方壁纸、全网资源、远程风格、版本公告全部由扩展真实消费。
7. 桌面、平板和手机无页面级横向溢出；核心流程满足 WCAG 2.1 AA。
8. 保留本地优先边界：私有本地图片、图标 Blob、设备运行状态和凭据不上传。

## 2. 已锁定的产品决策

- 后台受众：单人自托管管理员，不实现多管理员 RBAC 或商业多租户。
- 部署：路由器运行后端；公网只开放 HTTPS 插件 API；安装页和后台只允许本机/局域网。
- 后台布局：采用“分区式运维工作台”，资源编辑吸收列表 + 详情编辑器模式。
- 插件账号：允许开放注册，但必须完成邮件验证，并受限流和容量配额保护。
- 管理员初始化：首次启动进入 Web 安装向导，不再内置任何默认账号或密码。
- 冲突：三方自动合并非重叠修改；同一实体或同一设置的重叠修改由用户选择。
- 后台前端：Preact + Vite 构建，产物继续嵌入 Go 二进制；运行时仍为单容器、单服务。
- 远程风格：使用受限 CSS 包，禁止任意外链和危险规则；必须经过校验、预览、发布和回滚。
- GitHub Gist：仅作为手动备份/恢复通道，不与后端进行双向实时同步。

## 3. 系统边界与组件

### 3.1 网络边界

| 表面 | 暴露范围 | 认证 |
|---|---|---|
| `/health/live` | 本机、局域网或容器健康检查 | 无敏感数据 |
| `/health/ready` | 本机、局域网或容器编排 | 无敏感数据；返回数据库/迁移 readiness |
| `/api/v1/auth/register`、verify/resend/login/forgot/reset、`/api/v1/app/bootstrap` | HTTPS 公网域名 | 匿名 + 专用限流；一次性 token 端点另验 token |
| `/api/v1/auth/refresh` | HTTPS 公网域名 | rotating refresh token |
| 其余 `/api/v1/*` | HTTPS 公网域名 | 插件 access token |
| `/account/verify`、`/account/reset` | HTTPS 公网域名 | 一次性邮件 token；最小公开页面 |
| `/install/*` | 仅管理员 CIDR；仅未安装/重置时存在；除 strict loopback 外要求 HTTPS | bootstrap token 后建立安装会话；写操作加 CSRF |
| `/admin/*` | 仅管理员 CIDR，包括 shell 和所有静态资源；除 strict loopback 外要求 HTTPS | 可加载登录壳；业务数据不匿名返回 |
| `/api/admin/v1/auth/session`、`/api/admin/v1/auth/login` | 仅管理员 CIDR | pre-auth Cookie；登录写请求加 Origin/CSRF/JSON |
| 其余 `/api/admin/v1/*` | 仅管理员 CIDR | 管理员 Cookie；状态变更再加 Origin/CSRF/JSON |
| `/metrics` | 默认关闭；启用时仅本机/局域网 | 独立配置 |

反向代理只在直接对端属于 `trusted_proxies` 时读取转发头，并从右向左剥离可信代理；不再信任客户端预置的 `X-Forwarded-For` 最左值。非法 CIDR、代理或来源配置必须令服务启动失败。

监听地址、数据目录、管理员 CIDR 和 trusted proxies 属于启动级安全配置，只能通过环境变量或 CLI 修改，后台只读展示并生成重启指引；公网 URL、扩展 ID 白名单、SMTP、配额和保留策略存入数据库/secrets，可在后台校验后修改。安装前网络门只读取启动配置：管理员 CIDR 默认允许 loopback、RFC1918 和 ULA 私网范围，trusted proxies 默认空列表；访问者仍必须验证安装码。

### 3.2 后端模块

将当前大型 `server.go` / `store.go` 拆为职责明确的模块：

- `config`：启动配置、安装状态和安全校验。
- `auth`：管理员会话、插件用户注册/验证/登录/刷新/找回。
- `sync`：共享配置、设备、mutation、幂等、冲突和版本历史。
- `catalog`：官方壁纸、Web 资源、风格包及其发布版本。
- `release`：扩展版本公告与兼容范围。
- `admin`：后台专用查询和命令，不直接复用公网 DTO。
- `observability`：访问日志、管理员审计、健康状态和聚合指标。
- `maintenance`：备份、清理、容量水位和迁移。

模块通过服务接口调用 repository；HTTP handler 不直接拼 SQL，repository 不承担权限判断。

### 3.3 后台前端

新增独立的轻量 Preact 管理台源码，Vite 输出带内容哈希的静态文件，由 Go `embed` 内嵌。Docker 构建阶段增加 Node 构建，运行镜像仍只包含 Go 二进制和数据目录。

前端使用路由级数据加载、独立错误边界、Zod 响应验证和统一组件状态，不再在登录后用 `Promise.all` 拉取所有数据。

### 3.4 扩展端

将 2900 行级别的 `App.tsx` 按职责拆分：

- `profile-store`：共享配置和本地状态的读写、迁移、revision。
- `sync-controller`：outbox、后台 worker 通信、重试和冲突状态机。
- `auth-client`：登录、注册、验证、token 刷新与撤销。
- `catalog-client`：壁纸、风格和版本公告。
- `settings/*`：设置 UI 和领域组件。

新标签页只展示同步状态和发出变更事件；持久化同步由 MV3 Service Worker 执行。

## 4. 首次安装向导

### 4.1 可用条件

- 仅当数据库安装状态为 `uninitialized` 或迁移状态为 `requires_admin_reset` 时注册 `/install` 路由。
- 整个 `/install/*` 受管理员 CIDR 限制。
- 首次启动生成 128-bit 一次性安装码，写入容器日志并保存到数据卷内权限为 `0600` 的 `install-code` 文件；安装页必须先验证该码，验证成功后才建立安装会话。可用 `FULLPRO_INSTALL_CODE` 显式覆盖，便于自动化部署。
- 安装码验证按来源 IP 限流；建立安装会话不会消费安装码，只有安装完成或本机 CLI 主动轮换才令其失效，避免会话超时后永久锁死。迁移到 `requires_admin_reset` 时生成新码，绝不沿用旧管理员凭据。
- 安装会话使用短时 HttpOnly、SameSite=Strict Cookie 和 CSRF token。
- 安装完成后路由返回 404；只能通过本机 CLI 维护命令显式重新开启，不能在普通后台中一键重开。
- 安装模式分为 `fresh_install` 与 `admin_reset`：前者执行全部步骤；后者只重建管理员并撤销会话，保留插件用户、内容、同步数据、系统设置和 secrets。若管理员在 reset 中主动修改可选设置，最终复核页必须显示逐项差异。

### 4.2 向导步骤

1. **环境检查**：数据库目录可写、系统时间、磁盘空间、反代/HTTPS 状态、可信代理配置。
2. **管理员账号**：管理员邮箱、显示名和至少 12 位密码；禁止常见弱密码。
3. **公网 API**：外部 HTTPS 基础 URL、允许的扩展 ID、可选 Web 开发来源。
4. **邮件服务**：SMTP 主机、端口、TLS、发件人、用户名和密码；必须发送测试邮件成功才能启用开放注册。
5. **容量与保留**：最大用户数、单配置大小、数据库总水位、版本数量、日志保留和备份目录；展示推荐默认值。
6. **复核并安装**：展示网络暴露面和隐私边界，提交后进入可恢复的两阶段安装状态机。

推荐默认值：最大 100 个用户、单个共享配置 512 KiB、数据库软水位 1 GiB、每用户 50 个版本、访问日志 30 天、管理员审计 180 天、7 个日备份和 4 个周备份。达到软水位 90% 时停止新注册并告警，达到 100% 时拒绝会增长存储的写入但保留登录、读取、清理和备份。管理员可在安装时调整。

开放注册在安装开始时为关闭状态；关闭时允许跳过 SMTP，开启时必须成功向管理员邮箱发送测试邮件。安装会话空闲 30 分钟、最长 2 小时，过期前提示续期；刷新和后退保留非敏感字段及步骤状态。preflight、SMTP 或 complete 失败不得写安装标记，也不得清空非敏感输入；会话失效后可用仍有效的安装码重新建立会话，或通过本机 CLI 轮换安装码。

### 4.3 密钥与密码存储

- 管理员和插件密码使用版本化 Argon2id 哈希；旧 bcrypt 用户在成功登录时自动升级。
- SMTP 密码、Cookie 密钥和 token 派生密钥保存在数据卷内权限为 `0600` 的原子写入 secrets 文件，不写进普通设置表或日志。
- 安装完成时撤销旧数据库中的全部会话和固定管理员；不保留 `lucky / 2231` 的兼容入口。
- 安装 complete 先写入并 fsync 临时 secrets，再提交包含管理员、设置和 `installing` 状态的数据库事务，然后原子 rename secrets，最后把数据库状态提交为 `installed`。进程重启时按阶段幂等完成或回滚；只有 `installed` 后才返回成功并删除安装码文件。

## 5. 认证、注册与账号恢复

### 5.1 管理员认证

- 使用独立的 `/api/admin/v1/auth/*`，只接受管理员 Cookie，不返回 Bearer token。
- Cookie 为 HttpOnly、SameSite=Strict；公网反代配置下必须 Secure。
- 非 loopback 安装和管理地址在生产模式强制 HTTPS + Secure Cookie。`FULLPRO_ADMIN_INSECURE_HTTP=true` 仅允许开发/紧急恢复，概览持续显示红色告警，并且使用该选项的部署不能通过生产验收。
- 所有写请求校验 Origin、CSRF token 和 `application/json`。
- 管理员可查看并撤销活跃会话；修改密码后撤销其他全部会话。
- 登录、退出、凭据修改、会话撤销和安装操作写入管理员审计日志。

### 5.2 插件用户认证

开放注册流程：

1. `POST /api/v1/auth/register` 创建 `pending_verification` 用户并发送邮件。
2. 邮件中的一次性验证 token 只存哈希，30 分钟失效；验证成功后账号变为 `active`。
3. 登录签发 15 分钟 opaque access token 与 30 天 rotating refresh token；数据库仅存 token 哈希。
4. refresh token 每次使用后轮换；重放旧 token 时撤销同一 token family。
5. 忘记密码 token 30 分钟失效；重置密码后撤销全部现有会话。
6. 管理员可暂停、封禁或删除账号，并可撤销单个设备或全部会话。

验证和重置邮件指向公开的最小页面。token 放在 URL fragment 中，由页面以 POST 提交，避免进入反代与访问日志；页面不加载管理台代码，也不暴露后台导航。

密码最少 8 位，并检查弱密码；邮箱规范化和格式校验使用同一共享规则。

### 5.3 反滥用

- 注册、验证邮件重发、登录、刷新和找回分别使用有界、自动清理的限流桶。
- 关键限流键组合 IP、规范化邮箱、账号和路由，并返回 `Retry-After`。
- 未验证账号不能访问同步和远程资源。
- 达到用户数或存储配额时返回结构化 `QUOTA_EXCEEDED`，不继续创建数据。
- 管理员可以临时关闭注册，但已注册用户仍能登录。

## 6. 可靠同步协议

### 6.1 数据边界

上传的 `SharedProfile` 仅包含可跨设备数据：

- 分组及其顺序。
- 快捷方式的标题、标准化 URL、分组、图标来源描述和排序。
- 搜索引擎选择。
- 可移植的主题、密度、图标形状和已发布远程风格引用。
- 内置或远程壁纸引用，以及可移植的轮换偏好。

以下内容永不进入 `SharedProfile`、配置版本、普通导出或 GitHub Gist：

- `deviceId`、access/refresh token、同步错误和上次尝试时间。`deviceId` 只作为认证后的同步协议元数据发送给已配置后端；access token 只出现在 Authorization header，refresh token 只发送到 refresh/logout，且都不得进入日志。
- 当前设置页签、选择态、拖拽态、菜单状态和 `rotationHistory`。
- 本地图片 Blob、本地图标 Blob、缓存文件和本地 `assetId`。
- 指向某一设备本地资源的当前壁纸选择；其他设备使用已确认的便携 fallback。

本地数据分为 `SharedProfile`、`DeviceState`、`SyncMetadata`、`Credentials` 和 `LocalAssets` 五个存储域。

`SharedProfileV2` 是拒绝未知字段的封闭 schema，并由 Go 和 Zod 共同验证：

- `Shortcut.icon` 的 wire union 只允许 `auto`、已登记 builtin icon 或经过 HTTPS/来源校验的便携 remote descriptor；本地图标只保留在 `LocalAssets`，上传时使用用户确认的 `auto`/builtin fallback。
- `SharedProfile.wallpaper.portableSelected` 只允许 builtin 或已发布 catalog ID，轮换集合过滤所有 local 项；设备本地覆盖存入 `DeviceState.wallpaperOverride`。
- 后端同步、普通导出和 Gist 共用唯一的 `toSharedProfile()` allowlist serializer，并递归拒绝 token、`deviceId`、sync metadata、`kind: local` 和任何 `assetId`。

### 6.2 持久 outbox

- IndexedDB 成为 SharedProfile、base snapshot 和 outbox 的权威存储。每次逻辑变更在同一事务写入本地 profile revision 和 immutable outbox `{accountScope, profileId, mutationId, baseVersion, baseHash, schemaVersion, canonicalProfileBytes, firstDirtyAt, maxDueAt}`，提交后才更新 UI。
- `accountScope = canonicalApiOrigin + immutableServerUserId`；base、outbox、conflict 和 SyncMetadata 都按 accountScope + profileId 隔离。Worker 只发送 token subject 匹配的记录；退出冻结原账号队列，切换账号或 API origin 必须重新执行首次连接。
- Service Worker 是后端凭据和 refresh 的唯一所有者，页面通过消息请求认证调用。refresh 按 token family single-flight，并携带稳定 requestId；服务端允许“立即前一枚 token + 同一设备”在 60 秒恢复窗口内幂等取得既有 child token，避免 Worker 崩溃或并发刷新误判重放。
- `chrome.alarms` 只作为唤醒提示；任务、`nextAttemptAt`、`maxDueAt` 和退避状态全部持久化。Worker 每次启动及 `onStartup`、`onInstalled`、UI 消息时校验并重建 alarm。
- 默认安静期 3 分钟、max wait 10 分钟。10 分钟是浏览器运行、设备未休眠且网络可用时的发送目标；否则在下一次可用唤醒后立即发送。
- 浏览器启动、网络恢复、登录成功和 token 刷新后检查 outbox；网络恢复事件只是加速路径。
- 网络失败指数退避，带随机抖动，最大 30 分钟；尊重 `Retry-After`。
- 每账号最多一个 in-flight mutation；未发送编辑合并为一个 successor，使用新 mutationId 并保留最早 `firstDirtyAt`。重试复用完全相同的 canonical bytes 和 idempotency key。
- ACK 在一个 IndexedDB 事务中更新持久 base、删除已确认 mutation，并为 successor 重新定基。多页面本地写入使用 revision CAS 和广播通知，不能最后写入者静默覆盖。
- 非 loopback、非私有局域网地址只允许 HTTPS；用户手工输入公网 HTTP 后端时客户端直接拒绝发送凭据和配置。

### 6.3 服务端 CAS 与原子幂等

`PUT /api/v1/sync/profile` 请求包含：

```json
{
  "baseVersion": 12,
  "mutationId": "mut_...",
  "deviceId": "dev_...",
  "schemaVersion": 2,
  "resolvesConflictId": null,
  "profile": {}
}
```

服务端在一个 SQLite 事务内完成：

1. 要求 `Idempotency-Key` 必填且等于 body `mutationId`，占用 `(user_id, route, mutation_id)`。
2. 校验 request hash；同 key 不同 body 返回 409 `IDEMPOTENCY_MISMATCH`。
3. 比较 `baseVersion` 与当前版本。
4. 写当前 profile、profile version、mutation、device 和 sync attempt。
5. 写幂等响应并提交。

重复请求返回第一次响应并标记 `idempotentReplay: true`。完整响应 body 可在 24 小时后清理，但 `(userId, mutationId)` 的已应用版本/hash 去重证据至少保留到设备 90 天失效期限；重放不得再次执行。过期设备必须重新握手，不能继续发送陈旧 mutation。

精确 wire contract：

- 无 profile 的 GET 返回 `{profile:null, version:0, profileHash:null, schemaVersion:2}`。
- PUT 成功返回 `{profile, version, profileHash, schemaVersion, updatedAt, mutationId, idempotentReplay}`。
- CAS 失败原子创建/复用 conflict metadata，并返回 `PROFILE_CONFLICT` 及 `{conflictId, baseVersion, currentVersion, currentProfile, currentHash}`。
- 解决 mutation 必须携带 `resolvesConflictId`，成功写入时在同一事务关闭冲突；后台只显示冲突元数据/hash，不默认展开用户配置。
- 不支持的 schema 返回 `SCHEMA_VERSION_UNSUPPORTED` 和 `minSchemaVersion/maxSchemaVersion`，不得改写数据。
- 公开 restore 必须携带 `baseVersion`、`mutationId`，并走同一 CAS/幂等事务。

### 6.4 三方自动合并

客户端持久保存与 accountScope 绑定的最后确认 base snapshot。收到 `PROFILE_CONFLICT` 时，用版本化 JSON Pointer merge matrix 对 base、本机和服务器三份数据合并；`updatedAt` 只用于展示，不决定胜负：

| 路径族 | 合并单元 | 规则 |
|---|---|---|
| `/groups/byId/*` | 单个分组实体 | 单侧变化自动；双侧变化或删改冲突人工选择 |
| `/groups/order` | 稳定 ID 列表 | 不同 anchor 插入确定性合并；并发重排冲突 |
| `/shortcuts/byId/*` | 单个快捷方式实体 | 单侧变化自动；双侧变化、删改或父组冲突人工选择 |
| `/shortcuts/orderByGroup/*` | 每组稳定 ID 列表 | 与 group order 相同，结果必须通过引用校验 |
| `/search/*`、`/theme/*` | 标量或不可分配置段 | 一侧变化自动；双侧变化人工选择 |
| `/wallpaper/portable*` | 可移植壁纸选择/集合 | 不同集合项可合并；同一选择或并发重排冲突 |
| 未登记路径 | 无 | schema 校验拒绝，不做猜测式合并 |

- 分组和快捷方式按稳定 ID 合并；实体携带 `updatedAt` 和 `deletedAt` tombstone。
- 只在一侧变化的实体自动采用变化侧。
- 两侧修改不同实体时自动合并。
- 两侧修改同一实体或同一标量设置时生成显式冲突。
- 顺序使用稳定 ID 列表；不同 anchor 的并发插入按 `(anchor, createdAt, id)` 确定性合并，同一列表的并发重排进入冲突。
- 删除与修改同一实体时必须人工选择。
- 分组删除与另一侧对子项的新增、修改或迁移属于跨实体冲突；合并结果必须通过父子引用完整性校验。
- schema 不兼容时不自动合并。
- tombstone 至少保留 90 天，并且必须等所有活跃设备确认不低于删除版本后才能压缩；90 天未活动的设备标为 stale，下次连接执行完整重新同步，防止旧设备令已删除项目复活。

冲突界面固定保存 `{base, localAtConflict, remoteAtConflict}`，逐项展示可用选择，并允许导出三份 JSON。“两者都保留”只适用于可克隆实体，选择后生成新 ID 并修复引用；标量、顺序和删改冲突只能选本机或云端。冲突期间的新编辑进入独立 successor branch；解决固定快照后再重放该 branch。解决结果生成新 mutation，以最新服务器版本为 base，并携带 `resolvesConflictId`。

### 6.5 首次连接

- 登录后先用服务端返回的 immutable user ID 建立 accountScope，再检查该 scope 的 base/outbox；不得复用其他账号或 origin 的同步状态。
- `localEmpty` 由持久 provenance 标记“没有用户创作修改”，不能根据数组为空推断。
- 双方空：以 `baseVersion=0` 建立初始状态。
- 服务端无 profile、本机有用户数据：明确提示并上传本机共享配置。
- 服务端已有 profile、本机为 provenance empty：下载服务器配置。
- 双方都有数据但没有共同 base：展示两方差异，只允许用户选择本机、云端或对可克隆实体显式保留两者；不得推断删除或假装执行三方合并。
- 双方有共同 base：按 6.4 的三方合并规则处理。
- 登录成功不再直接写 `lastSyncedAt` 或显示“已同步”。只有服务器确认后才能显示成功状态。

### 6.6 GitHub Gist

GitHub 保持手动备份/恢复：

- 使用版本化 envelope `{format, formatVersion, schemaVersion, exportedAt, canonicalProfileSha256, profile}`，只保存通过 `toSharedProfile()` 的数据，不保存本地资源或凭据。
- 恢复先校验大小、hash、封闭 schema 和兼容范围。旧原始 Profile v1 只能经 legacy parser -> 本地迁移 -> `toSharedProfile()` 导入，并剥离 device/sync/local 字段。
- 覆盖前读取并比较远端 hash；本机没有 `lastSeenHash` 时一律视为未知远端修改并要求确认。写入后重新读取并验证。
- Gist PATCH 不提供本系统可依赖的原子 CAS，因此这里只提供最佳努力的覆盖检测，不承诺并发写零丢失，也不称为实时同步。
- 恢复必须显示摘要并要求确认，不改变后端同步 baseVersion，恢复后作为新的本地 mutation 同步。

## 7. 内容、风格和版本发布

### 7.1 统一生命周期

所有内容把 item 可见性与 revision 状态分开：

- item 可见性：`enabled`、`disabled`、`archived`。
- revision 状态：`draft`、`validating`、`ready`、`published`、`superseded`。

- 编辑已发布内容只创建或复用一个 draft，不直接覆盖 active revision。
- 校验失败回到 draft 并保留字段级错误；`ready` 才能发布。
- 发布原子切换 `activeRevisionId`，旧 published revision 变为 superseded。
- 回滚会把历史 revision 克隆为新 draft，重新校验并发布，不直接复活旧行。
- 归档前必须先停用；删除统一表现为归档。
- 所有创建、校验、发布、停用、回滚和归档写管理员审计。

| 类型 | 可用操作 | 扩展可见条件 |
|---|---|---|
| 官方壁纸 | 草稿、校验、发布、停用、回滚、归档 | item enabled 且 active revision published |
| Web 资源 | 草稿、provider 校验、发布、停用、回滚、归档 | item enabled 且 active revision published |
| 风格 | 草稿、安全校验、隔离预览、发布、停用、回滚、归档 | item enabled、published、兼容且 hash 通过 |
| 版本公告 | 草稿、校验、发布、停用、历史 | channel 的 active revision published |

### 7.2 壁纸

- 官方壁纸和 Web 资源都以列表为入口，详情编辑器支持预览、分辨率、标签、启停和 revision。
- 首个 Web provider 仅支持 UHD Paper；接口和数据库保留 provider adapter 边界，不再允许后台填写实际无法被扩展处理的任意 provider。
- URL 校验限制为 HTTPS、允许的 provider host、合理长度和唯一 variant ID。
- 后端不代理用户私有图片；远程资源 URL 仅返回给已验证登录用户。
- 扩展真正消费官方壁纸目录；公共 bootstrap 只返回非敏感版本和能力信息。

### 7.3 受限 CSS 风格包

风格包包含：

- `id`、名称、语义版本、描述、预览图。
- 最低/最高扩展版本、style schema 版本。
- 作用域 CSS、结构化 config、SHA-256。
- 发布状态、revision 和创建/发布时间。

CSS 校验规则：

- 所有选择器必须位于分配给该风格的 `.newtab-root[data-style-id="..."]` 作用域内。
- 只允许已登记的组件选择器和视觉属性。
- 禁止 `@import`、`@font-face`、`url()`、`expression()`、外部网络请求、行为属性和越过产品 z-index 体系的覆盖。
- 禁止隐藏安全/同步状态、伪造原生登录控件或覆盖设置对话框交互层。
- 管理台使用隔离预览 iframe 和示例数据验证桌面/手机状态。

扩展只应用已发布、版本兼容且 hash 校验通过的风格；失败时回退内置 `quark-flow`，不影响新标签页打开。

### 7.4 版本发布

- 版本记录按 channel + semver 唯一，支持 stable/beta。
- bootstrap 返回当前 channel、最低支持版本、schema 版本、更新说明和下载/商店链接。
- 扩展启动后低频检查并展示非阻塞更新提示；不尝试绕过浏览器扩展更新机制自动安装。
- 后台支持草稿、发布、停用和历史，不再只有追加表单。

## 8. 后台信息架构与交互

### 8.1 全局结构

采用五个主域：

1. **概览**：服务、数据库、邮件、备份和同步健康；异常队列优先于库存数字。
2. **用户与同步**：用户、设备、会话、配置版本、同步尝试和冲突。
3. **内容**：官方壁纸、Web 资源、风格；列表 + 详情编辑器。
4. **发布与审计**：版本发布、管理员操作审计、API/同步日志。
5. **安全与维护**：注册、来源白名单、代理状态、限流、保留、备份和安装信息。

侧栏只负责主域；域内使用与 canonical route 一一对应的二级导航，内容类型使用 URL 驱动的 tabs。每个路由只加载自身数据。

| 主域 | Canonical routes | 默认入口与主操作 | 空态 |
|---|---|---|---|
| 概览 | `/admin/overview` | 异常队列；下钻到带筛选的目标路由 | 显示“当前无待处理问题”和健康状态 |
| 用户与同步 | `/admin/users`、`/admin/users/:id`、`/admin/sync/attempts`、`/admin/sync/conflicts` | 搜索用户、撤销会话、诊断同步、解决冲突 | 解释如何完成首个插件注册/同步 |
| 内容 | `/admin/content/official`、`/admin/content/web`、`/admin/content/styles`、对应 `/:id` | 新建草稿、校验、预览、发布 | 解释支持的资源类型及创建入口 |
| 发布与审计 | `/admin/releases`、`/admin/audit/admin`、`/admin/audit/access` | 发布版本、查看管理员审计和 HTTP access log | 日志空态说明保留和采样规则 |
| 安全与维护 | `/admin/security`、`/admin/maintenance`、`/admin/backups`、`/admin/system` | 注册/来源/配额、备份恢复、健康检查 | 安装信息只读展示，不提供重开或安装码 |

同步 attempts/conflicts 只归“用户与同步”；“发布与审计”不重复提供同步日志，只通过 `requestId` 交叉链接。筛选、排序、时间范围、cursor 和选中实体写入 URL；从详情返回列表时恢复筛选和滚动位置。

### 8.2 概览

不再显示八个同质计数卡。首屏由以下内容组成：

- 紧凑状态条：API、SQLite、SMTP、最近备份、磁盘水位。
- “需要关注”队列：未解决冲突、连续同步失败、验证邮件失败、发布校验失败、安全配置警告。
- 24 小时同步摘要：成功率、401/409/429/5xx、P95 延迟和幂等重放。
- 最近发布与维护任务。

每项可下钻到已经带好筛选条件的目标页面。

### 8.3 用户与同步

用户列表支持搜索、状态、验证状态、最后活动、设备数和分页。用户详情默认只显示：

- 账号元数据和状态。
- 活跃设备/会话及撤销操作。
- 当前服务器版本、schema、profile 大小和分组/快捷方式数量摘要。
- 最近同步结果、错误 code、request ID 和版本时间线。

默认不显示用户完整快捷网址或 profile JSON。查看完整配置必须进行二次确认、给出原因并写管理员审计。

恢复旧版本先显示摘要和差异，再创建一个新版本；不回退版本号。

### 8.4 内容编辑器

- 左侧/主区是可筛选、分页的资源列表；右侧桌面详情面板或移动端独立详情页负责编辑。
- 明确显示“新建 / 编辑草稿 / 查看已发布版本”，不再依赖同 ID 隐式覆盖。
- 表单提供字段级校验、URL 类型、预览、脏数据离开保护和保存状态。
- 删除为归档；发布、回滚等高风险操作需要明确确认。
- 保存只刷新当前实体，不重新加载全部后台。

### 8.5 加载、错误与可访问性

- 路由级 skeleton；列表、详情和侧栏数据可独立失败和重试。
- 网络错误不等同于“未登录”；401 才进入会话恢复或登录。
- 成功使用短时 `role=status`；错误使用持久 inline alert。只有表单提交错误把焦点移到可聚焦错误摘要，并提供跳转到无效字段的链接；路由或分区加载失败保持当前焦点。
- 所有字段有可见 label；placeholder 只作示例。
- 登录后把焦点移到目标页标题。只有模态对话框和移动端导航抽屉使用 `inert` 与焦点锁定；桌面非模态详情面板不锁焦点。所有可关闭浮层支持 Escape 和焦点恢复。
- 可见焦点对比度不低于 3:1；正文和按钮文字达到 4.5:1。
- 保留现有安静浅色基调，但重新计算 accent 的 OKLCH 值，确保现代浏览器实际颜色通过 AA。
- 触控目标至少 44px；尊重 `prefers-reduced-motion`。

### 8.6 响应式

- `>= 1200px`：固定侧栏 + 宽内容；资源列表与详情并排。
- `768-1199px`：可折叠侧栏；帮助内容进入 inline disclosure；列表和详情按任务切换。
- `< 768px`：顶部栏 + 抽屉导航；当前任务先于全部导航展示。
- 所有断点要求 `document.scrollWidth == viewport width`；表格使用关键列 + 行详情，不依赖页面级横向滚动。
- 桌面并排视图与窄屏独立详情页共享相同可深链 URL；窄屏进入详情后，浏览器返回必须恢复列表筛选、排序、分页和滚动位置。
- 每类表格明确保留关键列，其余字段进入可键盘展开、带 `aria-expanded` 的行详情。

## 9. API 设计

### 9.1 版本和响应

新接口使用 `/api/v1` 和 `/api/admin/v1`。成功响应统一为：

```json
{
  "data": {},
  "requestId": "req_..."
}
```

错误响应统一为：

```json
{
  "error": {
    "code": "PROFILE_CONFLICT",
    "message": "云端配置已更新",
    "details": {}
  },
  "requestId": "req_..."
}
```

用户可恢复错误使用稳定 code；数据库原始错误不返回客户端。列表使用 cursor pagination，并返回 `nextCursor`。

### 9.2 主要公网接口

- `POST /api/v1/auth/register`
- `POST /api/v1/auth/verify-email`
- `POST /api/v1/auth/resend-verification`
- `POST /api/v1/auth/login`
- `POST /api/v1/auth/refresh`
- `POST /api/v1/auth/logout`
- `POST /api/v1/auth/forgot-password`
- `POST /api/v1/auth/reset-password`
- `GET /api/v1/me`
- `GET /api/v1/sync/profile`
- `PUT /api/v1/sync/profile`
- `GET /api/v1/sync/profile/versions`
- `POST /api/v1/sync/profile/versions/{id}/restore`
- `GET /api/v1/catalog/wallpapers/official`
- `GET /api/v1/catalog/wallpapers/web`
- `GET /api/v1/catalog/styles`
- `GET /api/v1/app/bootstrap`

### 9.3 主要后台接口

- 安装：status、preflight、SMTP test、complete。
- 管理员 auth：session、login、logout、password、sessions/revoke。
- 用户：list、detail、status、sessions、versions、restore、diagnostics。
- 同步：attempts、conflicts、conflict detail。
- 内容：list、draft create/update、validate、publish、disable、rollback、archive。
- 发布：release draft、publish、disable、history。
- 日志：access logs、admin audit、detail/export。
- 系统：health detail、settings、storage、backup create/list/restore、maintenance jobs。

完整版本以 `0.2.0` 发布；兼容旧 `/api/*` 的 adapter 在整个 `0.2.x` 周期保留，并为可安全转换的登录/读取接口返回旧 envelope，旧客户端收到弃用提示，在 `0.3.0` 删除。迁移前签发的 legacy bearer 仅允许读取自己的既有数据并按原到期时间失效，不签发 refresh token。旧版无 `baseVersion` 的 profile 写入和无验证注册返回 `426 SYNC_PROTOCOL_UPGRADE_REQUIRED`；schema v1 客户端不得写回 v2 的降级投影。固定管理员和不安全后台路由不提供兼容。

## 10. 数据模型与迁移

### 10.1 关键表

- `schema_migrations`、`installation_state`、`system_settings`。
- `admin_users`、`plugin_users`、`auth_sessions`、`email_verification_tokens`、`password_reset_tokens`。
- `devices`、`sync_profiles`、`profile_versions`、`sync_mutations`、`idempotency_keys`、`sync_attempts`、`sync_conflicts`。
- `catalog_items`、`catalog_revisions`、`style_revisions`、`release_revisions`。
- `access_logs`、`admin_audit_logs`、`maintenance_jobs`、`backup_records`。

敏感 token 只存哈希；日志不存密码、token、完整 profile 或私人 URL。

### 10.2 迁移规则

1. 升级前使用 SQLite online backup 创建带校验和的备份。
2. 用编号迁移替换启动时无版本 `CREATE TABLE IF NOT EXISTS`；升级在数据库副本上完成全部迁移与校验，随后关闭旧连接并原子换库。失败时删除迁移副本，原数据库保持不变。
3. 旧固定管理员不迁移密码，安装状态设为 `requires_admin_reset`，并撤销所有旧管理员会话。
4. 已存在的插件用户迁移为 `legacy_unverified`：在 `0.2.x` 可登录并读取自己的既有配置，但同步写入、邮件找回和其他依赖邮箱所有权的能力必须在完成验证后开放；管理员可在用户重新认证后触发验证或协助更换邮箱，不能批量直接标记 verified。
5. 旧 profile 原文先写只读 legacy backup，再投影为 `SharedProfile v2`；无法投影的本地字段保留在 legacy backup，不上传。
6. 旧风格导入为 `legacy_draft`，必须通过新校验后才能重新发布；扩展未拿到有效新包时回退内置风格。
7. 迁移成功后执行数据计数、外键、profile JSON 和 hash 校验；失败则停止服务并保留原数据库和备份。
8. 扩展 `0.2.0` 首次启动把 `chrome.storage` 中的旧完整 Profile 经 legacy parser 拆为 SharedProfileV2、DeviceState 和 SyncMetadata，并在一个 IndexedDB 事务写入权威存储；原 JSON 作为只读本地恢复副本保留到 `0.3.0`，不得直接上传。

## 11. 安全与可靠性基线

- 对 `/admin`、`/admin/`、`/admin/assets/*` 和 `/api/admin/*` 使用同一个网络门。
- 管理员登录也受网络门保护；插件登录永远不能签发管理员会话。
- CORS 只允许安装向导保存的精确 extension ID 和显式 Web 来源，不再匹配任意 `chrome-extension://`。
- 强制 JSON Content-Type、Origin/Referer、CSRF、CSP、`frame-ancestors 'none'`、`nosniff` 和安全 Referrer-Policy。
- 显式 `http.Server` 设置 header/read/write/idle timeout、最大 header、优雅 shutdown。
- `/health/live` 只表明进程存活；`/health/ready` 检查数据库和迁移状态，不吞掉错误。
- 访问日志按路由采样和保留；管理员写操作进入独立审计日志。
- 全局、IP、账号、路由和存储配额共同限制匿名 404、注册和 profile 写入造成的磁盘 DoS。
- SQLite 启用 WAL、busy timeout、外键和定期 checkpoint；写事务保持短小。
- 任何安全关键配置解析失败都 fail closed。

## 12. 维护与备份

- 每日自动备份，保留 7 个日备份和 4 个周备份；支持后台手动备份。
- 所有备份使用 SQLite online-backup API，并生成绑定数据库、实际风格/预览资产、schema 版本和逐文件校验和的版本化 manifest；不能只保存资产清单。
- 完整灾备包含 secrets；secrets 使用用户再次输入的恢复口令经 Argon2id 派生密钥后以 AES-256-GCM 加密，恢复口令和派生密钥都不落盘。data-only 恢复必须生成新密钥、撤销全部会话并要求重新配置 SMTP。
- 恢复先校验版本、hash、磁盘空间和迁移兼容性，然后进入维护模式、排空写入、关闭数据库、备份当前状态并原子换库；失败可换回原库。成功后进程正常退出，由明确配置的 Docker/supervisor restart policy 重启并执行 readiness 检查，不尝试在进程内自我拉起。
- 定时清理：过期 session/token、24 小时幂等响应缓存、超过设备失效期限的 applied-mutation 证据、超期访问日志、已归档草稿和超过上限的 profile 版本。
- 概览展示数据库大小、剩余空间、最近备份、最近清理和失败维护任务。

## 13. 测试与验收

### 13.1 后端

- fresh_install/admin_reset、安装码轮换、刷新/后退、30 分钟空闲/2 小时上限、失败重试、完成后关闭和重复安装拒绝；SMTP 跳过与启用注册两条分支都覆盖。
- `/admin/assets/` 和 `index.html` 从非管理员网络返回 403/404。
- 可信代理链无法用预置 XFF 伪造管理员 CIDR。
- 管理员无默认口令，插件账号不能获得管理员 Cookie。
- Cookie/CSRF/Origin/Content-Type 和安全响应头测试。
- 后台文本、版本字段和 URL 的 stored XSS、危险 scheme 与 CSP 回归测试。
- 邮件验证、重发、登录、刷新重放、找回、封禁和配额测试。
- CAS 冲突、conflictId/resolution、原子幂等、并发相同 mutation、key/body 不匹配、ACK 丢失超过 24 小时后重试和分层清理测试。
- 访问日志脱敏、管理员操作审计和保留测试。
- 内容 revision、校验、发布、回滚、归档和不安全 CSS 拒绝测试。
- 两阶段安装恢复、编号迁移副本、旧数据库升级、失败保持原库、完整/数据-only 备份恢复和 supervisor 重启契约测试。
- health/readiness、服务器 timeout 和优雅停机测试。

### 13.2 扩展

- SharedProfile revision 与 outbox 的 IndexedDB 原子写、崩溃恢复、多页面 revision CAS 和 successor 重新定基。
- outbox 在关闭新标签页、Service Worker 重启和浏览器重启后仍存在；alarm 丢失/延迟时可从持久 due time 重建。
- 3 分钟安静期、条件式 10 分钟 max wait、退避、Retry-After、完全相同 body/key 的 mutation 重试。
- 首次连接覆盖双方空、本机有、云端有、双方有共同 base、双方无共同 base和 provenance empty。
- 非重叠实体自动合并；同实体、删改、并发重排、父组删除/子项修改和引用完整性冲突进入人工解决。
- 冲突期间的新编辑进入 successor branch；“两者都保留”只克隆允许的实体并生成新 ID。
- accountScope 隔离、账号/API origin 切换、退出冻结队列和 stale 设备完整重同步。
- `deviceId` 只作为协议元数据；本地 asset、token、sync metadata 和轮播历史不能进入 SharedProfile、版本或导出。
- Service Worker refresh single-flight、前一 token 恢复窗口和持久化前崩溃恢复。
- 401 自动刷新或安全退出；非 JSON、超时、离线、413、429、5xx 有明确状态。
- 官方壁纸、Web 资源、风格和版本公告真实消费。
- 不兼容或 hash 错误的远程风格回退内置风格。
- GitHub 版本化 envelope、旧 v1 净化迁移、未知 lastSeenHash、写后验证、覆盖检测和凭据不进入导出配置。

### 13.3 后台与端到端

- 安装向导、管理员登录、用户检索/封禁、会话撤销、版本恢复。
- 同步尝试和冲突可从概览下钻到对应筛选结果。
- 壁纸/Web/风格覆盖草稿、校验、预览、发布、停用、回滚和归档；版本公告覆盖草稿、校验、发布、停用和历史。
- 单个接口失败只影响对应区域，可独立重试。
- 核心流程明确为：安装、用户管理、会话撤销、版本恢复、冲突诊断、内容发布/回滚和备份恢复；全部可用键盘完成。
- 自动 axe 检查无 critical/serious；人工键盘测试通过，并至少使用 NVDA + Chrome 完成一次读屏验证。
- 320x568、390x844、768x1024、1024x768、1199x800、1200x800、1280x720、1440x900 无页面级横向溢出；单测 767/768 与 1199/1200 边界。
- 400% zoom 按 320 CSS px 验证 reflow；所有操作、确认框和虚拟键盘场景不得被裁切。
- 支持当前及前一主版本桌面 Chrome/Edge，以及当前 Android Chrome 和 iOS Safari。
- 自动化对比度检查：正文 4.5:1、焦点和非文字控件 3:1。
- `prefers-reduced-motion`、空态、loading、错误和长文本状态截图。

### 13.4 性能目标

- 路由器空闲后端内存目标不高于 100 MiB；管理台静态资源 gzip 后目标不高于 250 KiB（不含图片）。
- 后台首个路由不请求其他路由的数据。
- 10,000 条访问日志和 1,000 个用户下列表仍使用分页和索引，不全量加载。
- 典型 profile PUT 的服务端 P95 目标低于 200ms（本地 SQLite、无网络时间）。

## 14. 交付顺序与发布门槛

实现拆为六个相互隔离、逐项验收的交付域：

1. **迁移与安全底座**：编号迁移、备份、安装向导、管理员认证、网络门、代理和安全头。
2. **开放注册与账号体系**：SMTP、验证、token 轮换、找回、限流、配额和用户管理。
3. **同步核心**：SharedProfile v2、原子 CAS/幂等、设备/attempt/conflict、扩展 outbox 和三方合并。
4. **后台工作台**：Preact shell、概览、用户与同步、日志、维护和完整响应式/A11y。
5. **内容与发布闭环**：壁纸 provider、受限风格 revision、版本公告及扩展消费。
6. **整体验收与切换**：旧 API adapter、旧数据迁移、全量 E2E、安全回归、性能和部署文档。

这些交付域是实现顺序，不是可对外发布的半成品版本。最终发布必须同时满足：

- 13.1-13.3 的所有验收项均为 MUST；最终发布要求全部 MUST 通过，并且没有未解决的 P0/P1 缺陷。
- 旧固定管理员和不安全静态路径不可访问。
- 多设备冲突测试证明不会静默丢数据。
- 安装、注册、同步、后台、内容发布、备份恢复的端到端流程全部通过。
- Docker ARM64 镜像、HTTPS 反代示例和升级/回滚手册完成。

## 15. 明确不包含

- 商业多租户、计费、客服工单和多管理员 RBAC。
- 任意远程 JavaScript、未受限 CSS 或风格内网络请求。
- 上传用户私有本地壁纸或图标 Blob 到后端。
- 后台直接抓取任意第三方网站；首期只实现受支持的 UHD Paper adapter。
- 绕过 Chrome/Edge 官方更新机制静默安装扩展。
- 对同一实体重叠修改进行不可解释的自动决策。
