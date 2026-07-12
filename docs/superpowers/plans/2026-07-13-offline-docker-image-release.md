# Offline Docker ARM64 Image Release Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make GitHub Actions publish a prebuilt Linux ARM64 Docker image archive that a Xiaomi router can deploy with `docker load -i` followed by a fixed `docker run` command.

**Architecture:** Extend the existing `package` job so the same GitHub Actions artifact contains the backend ZIP, extension ZIP, and a Docker image archive tagged `kekeio-tab:arm64`. Buildx exports Docker archive format, then the CI runner loads the archive, verifies its platform, launches it under QEMU, and polls the liveness endpoint before upload. The existing multi-architecture GHCR publication remains private and unchanged.

**Tech Stack:** GitHub Actions, Docker Buildx, QEMU/binfmt, Docker image archive format, PowerShell static verification, Markdown documentation.

## Global Constraints

- Do not make `ghcr.io/luckytomr/kekeio-tab` public.
- The offline artifact filename is exactly `kekeio-tab-docker-arm64.tar`.
- The image loaded from the archive is exactly `kekeio-tab:arm64`.
- The target platform is exactly `linux/arm64`; ARMv7 and MIPS remain unsupported.
- Production backend port remains exactly `9009`.
- Preserve the non-root UID/GID `10001`, `/data`, `/backups`, secure-cookie default, and existing health check from `backend/Dockerfile`.
- Do not run local application tests or local Docker builds; application build, `docker load`, platform inspection, and liveness verification run in GitHub Actions.
- Preserve user-owned untracked paths `.codegraph/` and `docker命令.txt`.
- Continue pinning every third-party GitHub Action to a full commit SHA.

---

## File Structure

- Modify `.github/workflows/publish.yml`: build, verify, upload, and release the Docker ARM64 archive.
- Modify `README.md`: describe the new offline artifact and the minimal load/run path.
- Modify `backend/README.md`: distinguish the complete Docker archive from the standalone ARM64 binary.
- Modify `backend/deploy/router/README.md`: add the router-first offline deployment procedure while retaining the private GHCR/Compose procedure.
- Modify `docs/superpowers/plans/2026-07-13-offline-docker-image-release.md`: check completed steps during execution.

### Task 1: Produce and verify the Docker ARM64 archive in GitHub Actions

**Files:**
- Modify: `.github/workflows/publish.yml`
- Modify: `docs/superpowers/plans/2026-07-13-offline-docker-image-release.md`

**Interfaces:**
- Consumes: `backend/Dockerfile`, existing QEMU/Buildx/build-push action SHAs, and the successful `verify` job.
- Produces: `release/kekeio-tab-docker-arm64.tar` containing the tag `kekeio-tab:arm64`.

- [x] **Step 1: Run a static assertion that demonstrates the artifact is missing**

Run from the repository root:

```powershell
$workflow = Get-Content -Raw '.github/workflows/publish.yml'
if ($workflow -notmatch [regex]::Escape('kekeio-tab-docker-arm64.tar')) {
  throw 'Expected failure: offline Docker archive is not configured'
}
```

Expected: command fails with `Expected failure: offline Docker archive is not configured`.

- [x] **Step 2: Add QEMU, Buildx, Docker archive export, and archive verification to `package`**

Insert these steps after `Build Windows and Linux ARM64 server binaries` and before `Create release archives`:

```yaml
      - name: Set up QEMU for offline ARM64 image
        uses: docker/setup-qemu-action@96fe6ef7f33517b61c61be40b68a1882f3264fb8 # v4

      - name: Set up Buildx for offline ARM64 image
        uses: docker/setup-buildx-action@bb05f3f5519dd87d3ba754cc423b652a5edd6d2c # v4

      - name: Build offline ARM64 Docker archive
        uses: docker/build-push-action@53b7df96c91f9c12dcc8a07bcb9ccacbed38856a # v7
        with:
          context: ./backend
          file: ./backend/Dockerfile
          platforms: linux/arm64
          tags: kekeio-tab:arm64
          outputs: type=docker,dest=${{ runner.temp }}/kekeio-tab-docker-arm64.tar

      - name: Verify offline ARM64 Docker archive
        shell: bash
        run: |
          set -euo pipefail
          archive="${RUNNER_TEMP}/kekeio-tab-docker-arm64.tar"
          test -s "${archive}"
          docker load -i "${archive}"
          test "$(docker image inspect kekeio-tab:arm64 --format '{{.Os}}/{{.Architecture}}')" = "linux/arm64"

          container_name="kekeio-tab-offline-verify"
          cleanup() {
            docker rm -f "${container_name}" >/dev/null 2>&1 || true
          }
          trap cleanup EXIT

          docker run -d \
            --name "${container_name}" \
            --platform linux/arm64 \
            -p 127.0.0.1:9009:9009 \
            kekeio-tab:arm64

          for attempt in $(seq 1 30); do
            if curl --fail --silent --show-error http://127.0.0.1:9009/health/live >/dev/null; then
              exit 0
            fi
            sleep 2
          done

          docker logs "${container_name}"
          exit 1
```

The verification uses the image defaults and anonymous CI-only volumes. It does not weaken the production HTTPS or administrator-network rules.

- [x] **Step 3: Move the verified tar into the release directory and upload it**

Replace `Create release archives` with:

```yaml
      - name: Create release archives
        run: |
          mkdir -p release
          mv "${RUNNER_TEMP}/kekeio-tab-docker-arm64.tar" release/kekeio-tab-docker-arm64.tar
          cd backend
          zip -qr ../release/kekeio-tab-backend.zip cmd internal admin-ui bin deploy Dockerfile .dockerignore .env.example go.mod go.sum README.md \
            -x 'admin-ui/node_modules/*' 'admin-ui/dist/*' 'admin-ui/coverage/*'
          cd ../extension
          zip -qr ../release/kekeio-tab-extension.zip dist
```

Replace the upload path with:

```yaml
          path: |
            release/*.zip
            release/*.tar
```

Replace the tagged-release command with:

```yaml
        run: gh release create "${GITHUB_REF_NAME}" release/*.zip release/*.tar --generate-notes --title "KeKeIO Tab ${GITHUB_REF_NAME}"
```

- [x] **Step 4: Run fresh structural validation**

Run:

```powershell
$workflow = Get-Content -Raw '.github/workflows/publish.yml'
$required = @(
  'kekeio-tab-docker-arm64.tar',
  'kekeio-tab:arm64',
  'outputs: type=docker',
  'docker load -i',
  'linux/arm64',
  '127.0.0.1:9009:9009',
  'release/*.tar'
)
foreach ($value in $required) {
  if (-not $workflow.Contains($value)) { throw "Missing workflow requirement: $value" }
}
if (($workflow | Select-String -Pattern 'uses:\s+[^\s]+@(?![0-9a-f]{40}(?:\s|$))' -AllMatches).Matches.Count -ne 0) {
  throw 'A GitHub Action is not pinned to a full SHA'
}
git diff --check
```

Expected: exit code `0` and no output from `git diff --check`.

- [x] **Step 5: Commit the workflow change**

```powershell
git add .github/workflows/publish.yml docs/superpowers/plans/2026-07-13-offline-docker-image-release.md
git diff --cached --check
git commit -m "ci: publish offline ARM64 Docker image"
```

Expected: commit contains only the workflow and checked plan progress.

### Task 2: Document the offline image path and exact router commands

**Files:**
- Modify: `README.md`
- Modify: `backend/README.md`
- Modify: `backend/deploy/router/README.md`
- Modify: `docs/superpowers/plans/2026-07-13-offline-docker-image-release.md`

**Interfaces:**
- Consumes: `kekeio-tab-docker-arm64.tar`, `kekeio-tab:arm64`, backend UID `10001`, port `9009`, `/data`, and `/backups` from Task 1 and `backend/Dockerfile`.
- Produces: one consistent operator procedure for Actions download → `docker load` → `docker run`.

- [x] **Step 1: Run a documentation assertion that demonstrates the new operator path is absent**

Run:

```powershell
$files = @('README.md', 'backend/README.md', 'backend/deploy/router/README.md')
foreach ($file in $files) {
  if ((Get-Content -Raw $file) -notmatch [regex]::Escape('kekeio-tab-docker-arm64.tar')) {
    throw "Expected failure: offline Docker instructions are missing from $file"
  }
}
```

Expected: command fails and names at least `README.md`.

- [x] **Step 2: Add the offline artifact contract to the root README**

Add this subsection under `## GitHub 自动构建与发布`, before the private GHCR login instructions:

````markdown
### 路由器 ARM64 离线镜像

Actions 的 `kekeio-tab-release` 产物包含可直接导入 Docker 的 `kekeio-tab-docker-arm64.tar`。它与 `bin/fullpro-server-linux-arm64` 不同：前者是完整 Docker image archive，后者只是裸可执行文件，不能用于 `docker load`。

```sh
docker load -i kekeio-tab-docker-arm64.tar
docker image inspect kekeio-tab:arm64
```

离线镜像不需要登录私有 GHCR。完整目录准备和 `docker run` 命令见路由器部署指南。
````

- [x] **Step 3: Add the backend archive distinction to `backend/README.md`**

Add this subsection immediately after the opening Docker deployment commands:

````markdown
### 离线 ARM64 Docker 镜像

从 Actions 下载 `kekeio-tab-release` 并解压后，`kekeio-tab-docker-arm64.tar` 已包含完整运行时和后端程序：

```sh
docker load -i kekeio-tab-docker-arm64.tar
```

导入后的镜像名为 `kekeio-tab:arm64`。`bin/fullpro-server-linux-arm64` 是供非 Docker 场景使用的裸二进制，不能执行 `docker load -i bin/fullpro-server-linux-arm64`。
````

- [x] **Step 4: Add the router-first offline procedure to `backend/deploy/router/README.md`**

Before the existing private-GHCR login section, add:

````markdown
## 2. 加载离线 ARM64 镜像并启动后端

从 Actions 的 `kekeio-tab-release` 下载并解压 `kekeio-tab-docker-arm64.tar`，复制到路由器后执行：

```sh
docker load -i kekeio-tab-docker-arm64.tar
mkdir -p /mnt/usb-24aeefbb/mi_docker/tab/data
mkdir -p /mnt/usb-24aeefbb/mi_docker/tab/backups
chown -R 10001:10001 /mnt/usb-24aeefbb/mi_docker/tab/data
chown -R 10001:10001 /mnt/usb-24aeefbb/mi_docker/tab/backups

docker rm -f kekeio-tab 2>/dev/null || true
docker run -d \
  --name kekeio-tab \
  --restart unless-stopped \
  -p 9009:9009 \
  -e FULLPRO_ADMIN_ALLOWED_CIDRS=192.168.50.0/24 \
  -v /mnt/usb-24aeefbb/mi_docker/tab/data:/data \
  -v /mnt/usb-24aeefbb/mi_docker/tab/backups:/backups \
  kekeio-tab:arm64
```

检查容器：

```sh
docker ps
docker logs --tail 100 kekeio-tab
```

`9009` 是后端 HTTP 上游端口。安装页、后台和正式扩展仍要求可信 HTTPS 入口；不要把 WAN `9009` 直接暴露到公网。需要无端口域名访问时，继续使用下文的 Caddy/Compose 或等价反向代理配置。
````

Rename the existing `## 2. 登录 GHCR 并启动` heading to `## 3. 私有 GHCR 与完整 Compose 部署`, then increment the following numbered headings by one so the final sequence is 1 through 7.

- [x] **Step 5: Run fresh documentation validation**

Run:

```powershell
$files = @('README.md', 'backend/README.md', 'backend/deploy/router/README.md')
foreach ($file in $files) {
  $content = Get-Content -Raw $file
  foreach ($required in @('kekeio-tab-docker-arm64.tar', 'docker load -i')) {
    if (-not $content.Contains($required)) { throw "$file is missing $required" }
  }
}
$router = Get-Content -Raw 'backend/deploy/router/README.md'
foreach ($required in @('kekeio-tab:arm64', 'FULLPRO_ADMIN_ALLOWED_CIDRS=192.168.50.0/24', '不要把 WAN `9009` 直接暴露到公网')) {
  if (-not $router.Contains($required)) { throw "Router guide is missing $required" }
}
git diff --check
```

Expected: exit code `0` and no whitespace errors.

- [x] **Step 6: Commit the documentation change**

```powershell
git add README.md backend/README.md backend/deploy/router/README.md docs/superpowers/plans/2026-07-13-offline-docker-image-release.md
git diff --cached --check
git commit -m "docs: add offline Docker deployment path"
```

Expected: commit contains only the three operator documents and checked plan progress.

### Task 3: Push, verify GitHub Actions, and inspect the downloadable artifact

**Files:**
- Modify: `docs/superpowers/plans/2026-07-13-offline-docker-image-release.md`

**Interfaces:**
- Consumes: the workflow and documentation commits from Tasks 1 and 2.
- Produces: a successful GitHub Actions run and a downloadable artifact containing all three required files.

- [ ] **Step 1: Run final local static verification without building the application**

Run:

```powershell
git diff --check
$status = git status --short
$allowed = @('?? .codegraph/', '?? "docker命令.txt"', '?? docker命令.txt')
$unexpected = @($status | Where-Object { $_ -notin $allowed })
if ($unexpected.Count -ne 0) { throw "Unexpected worktree changes: $($unexpected -join '; ')" }
git log -3 --oneline
```

Expected: only the two preserved user-owned untracked paths may remain; no application build or local Docker command runs.

- [ ] **Step 2: Push `main`**

```powershell
git push origin main
```

Expected: push succeeds and reports the new `main` tip.

- [ ] **Step 3: Find and watch the run for the exact pushed commit**

Run:

```powershell
$head = git rev-parse HEAD
$run = $null
for ($attempt = 0; $attempt -lt 24 -and -not $run; $attempt++) {
  $runs = gh run list --workflow publish.yml --branch main --limit 10 --json databaseId,headSha,status,conclusion | ConvertFrom-Json
  $run = $runs | Where-Object { $_.headSha -eq $head } | Select-Object -First 1
  if (-not $run) { Start-Sleep -Seconds 5 }
}
if (-not $run) { throw "No workflow run found for $head" }
gh run watch $run.databaseId --exit-status
```

Expected: `Verify source`, `Build release archives`, and `Publish GHCR image` succeed; tagged-release creation remains skipped on a normal `main` push.

- [ ] **Step 4: Download and inspect the exact Actions artifact**

Run:

```powershell
$head = git rev-parse HEAD
$run = (gh run list --workflow publish.yml --branch main --limit 10 --json databaseId,headSha,conclusion | ConvertFrom-Json | Where-Object { $_.headSha -eq $head } | Select-Object -First 1)
if (-not $run -or $run.conclusion -ne 'success') { throw 'The exact workflow run is not successful' }
$artifactDir = Join-Path $env:TEMP ("kekeio-tab-artifact-" + (Get-Date -Format 'yyyyMMddHHmmss'))
New-Item -ItemType Directory -Path $artifactDir | Out-Null
gh run download $run.databaseId --name kekeio-tab-release --dir $artifactDir
$required = @(
  'kekeio-tab-backend.zip',
  'kekeio-tab-extension.zip',
  'kekeio-tab-docker-arm64.tar'
)
foreach ($name in $required) {
  $file = Join-Path $artifactDir $name
  if (-not (Test-Path -LiteralPath $file -PathType Leaf)) { throw "Artifact is missing $name" }
  if ((Get-Item -LiteralPath $file).Length -le 0) { throw "Artifact file is empty: $name" }
}
Get-ChildItem -LiteralPath $artifactDir | Select-Object Name,Length
```

Expected: all three files exist and have nonzero sizes. Do not load the ARM64 image locally; the workflow already verified loading and liveness under QEMU.

- [ ] **Step 5: Check the completed plan and commit it**

Mark every checkbox in this plan complete, then run:

```powershell
git add docs/superpowers/plans/2026-07-13-offline-docker-image-release.md
git diff --cached --check
git commit -m "docs: complete offline image release plan"
git push origin main
```

Expected: the plan completion commit pushes successfully. Its documentation-only workflow run may be allowed to finish normally; the previously verified implementation run remains the release evidence.

## Handoff Command

After the successful artifact inspection, give the router operator exactly this deployment sequence:

```sh
docker load -i kekeio-tab-docker-arm64.tar
mkdir -p /mnt/usb-24aeefbb/mi_docker/tab/data
mkdir -p /mnt/usb-24aeefbb/mi_docker/tab/backups
chown -R 10001:10001 /mnt/usb-24aeefbb/mi_docker/tab/data
chown -R 10001:10001 /mnt/usb-24aeefbb/mi_docker/tab/backups
docker rm -f kekeio-tab 2>/dev/null || true
docker run -d \
  --name kekeio-tab \
  --restart unless-stopped \
  -p 9009:9009 \
  -e FULLPRO_ADMIN_ALLOWED_CIDRS=192.168.50.0/24 \
  -v /mnt/usb-24aeefbb/mi_docker/tab/data:/data \
  -v /mnt/usb-24aeefbb/mi_docker/tab/backups:/backups \
  kekeio-tab:arm64
docker logs --tail 100 kekeio-tab
```
