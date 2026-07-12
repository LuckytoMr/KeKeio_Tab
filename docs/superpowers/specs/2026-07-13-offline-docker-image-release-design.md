# KeKeIO Tab 离线 Docker ARM64 镜像发布设计

## 目标

GitHub Actions 在每次有效发布构建中生成一个已经完成应用编译和镜像封装的 Linux ARM64 Docker 镜像归档。路由器不需要访问私有 GHCR、不需要安装 Go 或 Node.js，也不需要在本地执行 `docker build`；操作者只需下载、解压、执行 `docker load -i`，然后执行固定的 `docker run` 命令。

## 非目标

- 不公开现有 GHCR 包；多架构 GHCR 发布继续保持私有。
- 不改变后端生产监听端口 `9009`、持久化目录或安全边界。
- 不用 OCI 专用归档替代 Docker `save` 格式，以免旧版路由器 Docker 无法直接加载。
- 不移除现有 Windows、Linux ARM64 二进制包或浏览器扩展包。

## 发布产物

现有 `kekeio-tab-release` Actions artifact 增加第三个文件：

```text
kekeio-tab-backend.zip
kekeio-tab-extension.zip
kekeio-tab-docker-arm64.tar
```

`kekeio-tab-docker-arm64.tar` 使用 Docker image archive 格式，内部镜像标签固定为：

```text
kekeio-tab:arm64
```

固定标签让离线部署命令不依赖 GitHub 提交 SHA，也不会误用私有 GHCR 地址。外层 GitHub Actions artifact 仍会以 ZIP 下载，因此不再额外 gzip 镜像 tar，避免旧版 Docker 对压缩格式支持不一致。

## GitHub Actions 数据流

1. `verify` 继续测试并构建前端、扩展和 Go 后端。
2. `package` 在 `verify` 成功后继续生成后端和扩展 ZIP。
3. `package` 设置 QEMU 与 Buildx，使用仓库的 `backend/Dockerfile` 构建 `linux/arm64` 镜像。
4. Buildx 通过 Docker exporter 直接输出 `release/kekeio-tab-docker-arm64.tar`，镜像标签为 `kekeio-tab:arm64`。
5. Actions 将 tar 加载回当前 runner，检查镜像架构和标签，然后启动容器并轮询 `/health/live`。
6. 只有加载、架构检查和健康检查全部成功，`upload-artifact` 才上传三个发布文件。
7. `v*` 标签创建 GitHub Release 时同时上传 ZIP 和 tar；普通 `main` 推送仍可从 Actions artifact 下载完整产物。

现有 `publish-image` job 保持不变，继续向私有 GHCR 发布 `linux/amd64` 与 `linux/arm64` 多架构镜像。

## CI 验证和清理

离线镜像验证在 GitHub runner 中完成，不在用户路由器或本地工作站执行。验证容器使用临时数据卷和非冲突宿主端口。工作流通过 shell trap 或无条件清理步骤删除验证容器，避免失败时留下运行资源。

验证至少覆盖：

- `docker load -i release/kekeio-tab-docker-arm64.tar` 成功。
- `docker image inspect kekeio-tab:arm64` 返回 Linux ARM64。
- 容器以镜像默认入口启动，并在超时前通过 `http://127.0.0.1:9009/health/live`。
- tar 文件存在且非空，artifact 上传缺失时直接失败。

## 路由器部署

操作者从 Actions artifact 或带标签的 GitHub Release 下载并解压 `kekeio-tab-docker-arm64.tar`，然后执行：

```sh
docker load -i kekeio-tab-docker-arm64.tar
```

创建持久化目录并启动：

```sh
mkdir -p /mnt/usb-24aeefbb/mi_docker/tab/data
mkdir -p /mnt/usb-24aeefbb/mi_docker/tab/backups
chown -R 10001:10001 /mnt/usb-24aeefbb/mi_docker/tab/data
chown -R 10001:10001 /mnt/usb-24aeefbb/mi_docker/tab/backups

docker run -d \
  --name kekeio-tab \
  --restart unless-stopped \
  -p 9009:9009 \
  -e FULLPRO_ADMIN_ALLOWED_CIDRS=192.168.50.0/24 \
  -v /mnt/usb-24aeefbb/mi_docker/tab/data:/data \
  -v /mnt/usb-24aeefbb/mi_docker/tab/backups:/backups \
  kekeio-tab:arm64
```

镜像内已经包含监听地址、数据库位置、备份位置、Cookie 安全策略、非 root 用户和健康检查的默认值，因此部署命令不重复声明这些环境变量。管理员 CIDR 必须由部署者按真实局域网配置；数据卷必须保留，确保重建容器后数据库、密钥和备份仍然存在。

## 更新与回滚

更新时先下载新的 tar，再执行 `docker load -i`。同名标签会指向新镜像；删除并按同一命令重建容器即可继续使用原持久化目录。需要回滚时，操作者应保留上一版 tar，重新加载旧归档并重建容器。数据库迁移和备份策略继续由应用现有生产逻辑负责。

## 文档范围

根 README、后端 README 与路由器部署 README 都应新增“离线 ARM64 镜像”入口，明确区分：

- `kekeio-tab-docker-arm64.tar` 是供 `docker load` 使用的完整镜像。
- `fullpro-server-linux-arm64` 是裸二进制，不能用于 `docker load`。
- GHCR 保持私有，离线安装不需要 `docker login`。
