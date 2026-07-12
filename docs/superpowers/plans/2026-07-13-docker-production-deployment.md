# KeKeIO Tab Docker Production Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the backend directly deployable on an amd64/arm64 router with Docker, use internal port `8881`, serve `https://tab.kekeio.com` without a visible port, and remove all Node.js 20 GitHub Actions warnings.

**Architecture:** A non-root Go backend listens on `:8881` in a private, fixed-address Docker network. Caddy terminates public TLS on container ports `80/443`, filters public and private-management paths, and proxies to `backend:8881`; router DNAT may map WAN `80/443` to high host ports `8080/8443` without changing the client URL.

**Tech Stack:** Docker Compose, Caddy 2, Go 1.25, SQLite, Node.js 24 LTS, pnpm 11.6.0, GitHub Actions, GHCR.

## Global Constraints

- Production backend port is exactly `8881`; public URL remains exactly `https://tab.kekeio.com`.
- Docker is the only supported production backend deployment target.
- Do not expose backend port `8881` through WAN forwarding.
- Do not run local application, unit, integration, or container tests in this task; perform static/configuration checks only.
- Keep the extension ZIP and tagged GitHub Release flow.
- Do not modify generated admin UI assets by hand.
- Preserve unrelated user files and do not include `.codegraph/` in the change.

---

### Task 1: Align the production runtime and image with port 8881 and Node.js 24

**Files:**
- Modify: `backend/cmd/fullpro-server/main.go:38`
- Modify: `backend/internal/server/store.go`
- Modify: `backend/internal/server/backup_scheduler.go`
- Modify: `backend/internal/server/backup_scheduler_test.go`
- Modify: `backend/admin-ui/src/pages/auth.tsx`
- Modify: `backend/admin-ui/src/pages/auth.test.tsx`
- Modify: `backend/Dockerfile:1-32`
- Modify: `backend/.env.example:1-10`
- Modify: `backend/.dockerignore`

**Interfaces:**
- Produces: backend HTTP service at container port `8881`; liveness URL `http://127.0.0.1:8881/health/live`; writable mount points `/data` and `/backups` owned by UID/GID `10001` in the image; fail-fast backup directory override.

- [ ] **Step 1: Change only the production default listener**

```go
addr := env("FULLPRO_ADDR", ":8881")
```

Keep the explicitly isolated local `dev` listener at `127.0.0.1:8787`.

- [ ] **Step 1b: Add and validate the Docker backup override**

Read `FULLPRO_BACKUP_DIRECTORY` before serving, require an absolute path, create it if needed, then verify write, `fsync`, close and delete operations. The override takes precedence over the value persisted by the installation wizard. Set the Docker/UI default to `/backups` and cover precedence with a Go test without running it locally in this task.

- [ ] **Step 2: Update the Docker build and runtime contract**

Use `node:24-alpine`, create and own both persistent directories, set secure production defaults, expose `8881`, and make the image health check a liveness check:

```dockerfile
FROM --platform=$BUILDPLATFORM node:24-alpine AS admin-ui-build
# existing build steps remain unchanged

FROM alpine:3.24
RUN apk add --no-cache wget && adduser -D -H -u 10001 fullpro
WORKDIR /app
COPY --from=build /out/fullpro-server /usr/local/bin/fullpro-server
RUN mkdir -p /data /backups && chown -R fullpro:fullpro /data /backups
USER fullpro
ENV FULLPRO_ADDR=:8881
ENV FULLPRO_DB=/data/fullpro.db
ENV FULLPRO_BACKUP_DIRECTORY=/backups
ENV FULLPRO_COOKIE_SECURE=true
ENV FULLPRO_HEALTHCHECK_URL=http://127.0.0.1:8881/health/live
EXPOSE 8881
VOLUME ["/data", "/backups"]
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=3s --start-period=30s --retries=3 \
	CMD wget -q -T 2 -O /dev/null "$FULLPRO_HEALTHCHECK_URL" || exit 1
CMD ["fullpro-server"]
```

- [ ] **Step 3: Make the environment example production-safe**

Set `FULLPRO_ADDR=:8881` and `FULLPRO_HEALTHCHECK_URL=http://127.0.0.1:8881/health/live`. Remove active localhost values for both origin override variables; leave one commented example explaining that `FULLPRO_API_ALLOWED_ORIGINS` overrides installation settings.

- [ ] **Step 4: Exclude deployment documentation from the Docker build context**

Append `deploy` to `backend/.dockerignore`; deployment files are distributed in the release archive and are not needed by `COPY . .` in the Go build stage.

- [ ] **Step 5: Run static checks**

Run:

```powershell
rg -n "FULLPRO_ADDR|FULLPRO_HEALTHCHECK_URL|EXPOSE|FROM node:" backend/Dockerfile backend/.env.example backend/cmd/fullpro-server/main.go
```

Expected: production defaults and image references use `8881` and Node.js 24; only the explicitly local `dev` code still uses `8787`.

---

### Task 2: Add an executable router Compose and Caddy deployment

**Files:**
- Create: `backend/deploy/router/compose.yaml`
- Create: `backend/deploy/router/compose.host-proxy.yaml`
- Create: `backend/deploy/router/Caddyfile`
- Create: `backend/deploy/router/router.env.example`
- Create: `backend/deploy/router/README.md`

**Interfaces:**
- Consumes: backend port `8881`, `/health/live`, `/health/ready`, UID/GID `10001`.
- Produces: services `backend` and `caddy`; Docker network `kekeio-tab-edge`; configurable Caddy host ports `8080/8443`; public domain environment variable `KEKEIO_DOMAIN`; optional loopback backend port override for a host proxy.

- [ ] **Step 1: Create the Compose topology**

The backend must use a required immutable GHCR tag/digest, fixed network addresses, pre-created data/backup bind mounts, log rotation, no host port by default, required exact `FULLPRO_TRUSTED_PROXIES`, and no CORS environment override. Caddy must wait for liveness, map high host ports to container `80/443`, persist certificate state, and receive required exact management networks. A separate override may bind backend only to `127.0.0.1:8881` for a host proxy.

- [ ] **Step 2: Create the Caddy route policy**

Use this route contract:

```caddyfile
{$KEKEIO_DOMAIN} {
  @management_root {
    remote_ip {$KEKEIO_ADMIN_NETWORKS}
    path /
  }
  @management {
    remote_ip {$KEKEIO_ADMIN_NETWORKS}
    path /admin /admin/* /install /install/* /api/admin/*
  }

  @public path /api/v1/* /account/verify /account/reset /account/assets/* /health/live /health/ready
  @public_root path /

  route {
    redir @management_root /admin 302
    reverse_proxy @public_root backend:8881 {
      rewrite /health/live
    }
    reverse_proxy @management backend:8881
    reverse_proxy @public backend:8881
    respond 404
  }
}
```

- [ ] **Step 3: Provide complete environment defaults**

`router.env.example` must leave the required backend image empty so deployment fails until the operator selects an actually published immutable tag; it must also contain the exact Caddy tag, fixed official domain, host bind ports, persistent directory paths, required smallest LAN CIDRs/trusted proxy, and non-conflicting `/29` Docker subnet inputs used by `compose.yaml`.

- [ ] **Step 4: Document first deployment and port forwarding**

Document GHCR login, router architecture, ext4/local filesystem requirement, UID/GID `10001` directory preparation, A/AAAA rules, IPv4 DNAT `80→8080` and `443→8443`, optional UDP 443, NAT loopback/split DNS, first installation, `/backups`, upgrade by immutable tag/digest, rollback, and WAN verification that management paths return 404.

- [ ] **Step 5: Perform non-running configuration checks**

Only if Docker Compose is installed, inject a syntactically valid non-published image reference for parsing, then render the configuration without starting or pulling anything:

```powershell
$env:KEKEIO_IMAGE='ghcr.io/example/kekeio-tab:sha-0000000000000000000000000000000000000000'
docker compose --env-file backend/deploy/router/router.env.example -f backend/deploy/router/compose.yaml config
```

Only if Caddy is installed, explicitly provide the placeholders that Compose normally passes into the container before validating the Caddyfile:

```powershell
$env:KEKEIO_DOMAIN='tab.kekeio.com'
$env:KEKEIO_ADMIN_NETWORKS='192.168.50.0/24'
caddy validate --config backend/deploy/router/Caddyfile --adapter caddyfile
```

If a tool is absent, report the check as skipped rather than installing or starting anything.

---

### Task 3: Move the publish workflow fully to Node.js 24-compatible Actions

**Files:**
- Modify: `.github/workflows/publish.yml:16-187`

**Interfaces:**
- Consumes: `backend/Dockerfile`, `backend/deploy/router/*`.
- Produces: Node.js 24 verification/package jobs, Node.js 24 Action runtimes, amd64/arm64 GHCR image, and a backend ZIP containing `deploy/router`.

- [ ] **Step 1: Pin the current Node.js 24-compatible Actions by full SHA**

Use these immutable references with major-version comments:

```yaml
actions/checkout@df4cb1c069e1874edd31b4311f1884172cec0e10 # v6
actions/setup-node@48b55a011bda9f5d6aeb4c2d9c7362e8dae4041e # v6
actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6
actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7
actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8
docker/setup-qemu-action@96fe6ef7f33517b61c61be40b68a1882f3264fb8 # v4
docker/setup-buildx-action@bb05f3f5519dd87d3ba754cc423b652a5edd6d2c # v4
docker/login-action@af1e73f918a031802d376d3c8bbc3fe56130a9b0 # v4
docker/metadata-action@dc802804100637a589fabce1cb79ff13a1411302 # v6
docker/build-push-action@53b7df96c91f9c12dcc8a07bcb9ccacbed38856a # v7
```

- [ ] **Step 2: Set project Node.js to 24 LTS**

Add top-level `NODE_VERSION: '24'` and use `node-version: ${{ env.NODE_VERSION }}` in both jobs.

- [ ] **Step 3: Include deployment assets in the backend archive**

Add `deploy` to the backend `zip` input list so a tagged release contains `deploy/router/compose.yaml`, `Caddyfile`, environment example, and instructions.

- [ ] **Step 4: Preserve intentional Release skipping**

Keep `if: startsWith(github.ref, 'refs/tags/v')`. Do not turn an ordinary `main` push or empty `workflow_dispatch` into a release.

- [ ] **Step 5: Scan the workflow**

Run:

```powershell
rg -n "uses:|node-version|deploy" .github/workflows/publish.yml
```

Expected: no `@v4` artifact Action, no Docker Action v3/old SHA, and no project Node.js 22.

---

### Task 4: Rewrite deployment documentation around Docker

**Files:**
- Modify: `README.md:14-52`
- Modify: `backend/README.md:5-136`
- Modify: `docs/architecture.md`

**Interfaces:**
- Consumes: the exact Compose variables, Caddy routes, port mappings, and health semantics created in Tasks 1-2.
- Produces: one consistent operator path from GHCR pull to `https://tab.kekeio.com`.

- [ ] **Step 1: Make Docker the primary backend path in the root README**

Move local development behind a clearly non-production note, link the runnable router deployment, and show the external/internal port chain:

```text
https://tab.kekeio.com:443 -> router WAN 443 -> host 8443 -> Caddy 443 -> backend:8881
```

- [ ] **Step 2: Update production port references**

Replace production install, admin, environment, Docker mapping, Caddy upstream, and SSH/upstream examples from `8787` to `8881`; retain only clearly labelled local-dev and historical test examples at `8787`.

- [ ] **Step 3: Correct public account routes and health semantics**

Add `/account/assets/*` to every proxy whitelist. Explain that Docker checks `/health/live`, while `/health/ready` remains the installation/maintenance readiness signal.

- [ ] **Step 4: Add router operational warnings**

Document that DNS does not forward ports, `EXPOSE` does not publish ports, WAN `80/443` may map to high internal ports, IPv6 needs a real standard-port path, and backend `8881` must never be WAN-forwarded.

---

### Task 5: Perform the requested static-only verification and handoff

**Files:**
- Verify all modified and created files.

**Interfaces:**
- Consumes: all prior tasks.
- Produces: a clean, reviewable patch and explicit list of checks run/skipped.

- [ ] **Step 1: Scan for stale production references**

Run targeted searches for `8787`, Node.js 22, artifact v4, Docker Action v3, and the three old Docker Action SHAs. Classify any remaining `8787` as local development, a historical migration fixture, or an arbitrary request source port.

- [ ] **Step 2: Check patch formatting**

Run:

```powershell
git diff --check
git status --short
git diff --stat
```

Expected: no whitespace errors; `.codegraph/` remains outside the patch.

- [ ] **Step 3: Review the final diff manually**

Confirm the four port layers are consistent: Go `:8881`, image `EXPOSE 8881`, Compose `backend:8881`, and router WAN `80/443` to Caddy only. Confirm the public extension URL remains `https://tab.kekeio.com`.

- [ ] **Step 4: Do not commit or push without an explicit user request**

Leave the completed patch in the shared workspace and report exact files and router rules. GitHub Actions will perform the full tests and multi-architecture build after the user publishes the changes.
