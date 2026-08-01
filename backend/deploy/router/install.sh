#!/bin/sh

set -eu

APP_IMAGE="${KEKEIO_IMAGE:-kekeio-tab:arm64}"
CADDY_IMAGE="${KEKEIO_CADDY_IMAGE:-caddy:2.11.4-alpine}"
CLOUDFLARED_IMAGE="${KEKEIO_CLOUDFLARED_IMAGE:-cloudflare/cloudflared:2026.7.3}"
DOMAIN="${KEKEIO_DOMAIN:-tab.kekeio.com}"
EDGE_NETWORK="${KEKEIO_DOCKER_NETWORK:-kekeio-tab-edge}"
DOCKER_SUBNET="${KEKEIO_DOCKER_SUBNET:-172.30.88.0/29}"
CADDY_IP="${KEKEIO_CADDY_IP:-172.30.88.2}"
BACKEND_IP="${KEKEIO_BACKEND_IP:-172.30.88.3}"
ORIGIN_PORT="${KEKEIO_TUNNEL_ORIGIN_PORT:-18081}"
MANAGED_LABEL="io.kekeio.managed-by"
MANAGED_VALUE="router-one-click-installer"

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
IMAGE_ARCHIVE="${KEKEIO_IMAGE_ARCHIVE:-${SCRIPT_DIR}/images.tar}"
IMAGE_CHECKSUM="${IMAGE_ARCHIVE}.sha256"
RUNTIME_DIR="${KEKEIO_RUNTIME_DIR:-${SCRIPT_DIR}/runtime}"
DATA_DIR="${RUNTIME_DIR}/data"
BACKUP_DIR="${RUNTIME_DIR}/backups"
SECRETS_DIR="${RUNTIME_DIR}/secrets"
TOKEN_FILE="${SECRETS_DIR}/cloudflare-tunnel-token"
STATE_FILE="${RUNTIME_DIR}/install.env"
SETTINGS_FILE="${RUNTIME_DIR}/cloudflare-settings.txt"
CADDYFILE="${SCRIPT_DIR}/Caddyfile.tunnel"
REPLACE_UNMANAGED=0
DEPLOYMENT_COMPLETE=0
CREATED_CONTAINERS=""

log() {
  printf '%s\n' "[KeKeIO] $*"
}

die() {
  printf '%s\n' "[KeKeIO] 错误：$*" >&2
  exit 1
}

need_command() {
  command -v "$1" >/dev/null 2>&1 || die "缺少命令：$1"
}

is_ipv4() {
  value=$1
  old_ifs=$IFS
  IFS=.
  # 按点号拆分已校验的 IPv4，必须保留受控分词。
  # shellcheck disable=SC2086
  set -- $value
  IFS=$old_ifs
  [ "$#" -eq 4 ] || return 1
  for octet in "$@"; do
    case "$octet" in
      ''|*[!0-9]*) return 1 ;;
    esac
    [ "$octet" -ge 0 ] 2>/dev/null || return 1
    [ "$octet" -le 255 ] 2>/dev/null || return 1
  done
}

is_ipv4_cidr() {
  value=$1
  case "$value" in
    */*) ;;
    *) return 1 ;;
  esac
  address=${value%/*}
  prefix=${value#*/}
  is_ipv4 "$address" || return 1
  case "$prefix" in
    ''|*[!0-9]*) return 1 ;;
  esac
  [ "$prefix" -ge 1 ] 2>/dev/null || return 1
  [ "$prefix" -le 32 ] 2>/dev/null || return 1
}

ipv4_to_int() {
  value=$1
  old_ifs=$IFS
  IFS=.
  # 按点号拆分已校验的 IPv4，必须保留受控分词。
  # shellcheck disable=SC2086
  set -- $value
  IFS=$old_ifs
  printf '%s\n' "$((($1 << 24) | ($2 << 16) | ($3 << 8) | $4))"
}

int_to_ipv4() {
  value=$1
  printf '%s.%s.%s.%s\n' \
    "$(((value >> 24) & 255))" \
    "$(((value >> 16) & 255))" \
    "$(((value >> 8) & 255))" \
    "$((value & 255))"
}

cidr_network() {
  value=$1
  address=${value%/*}
  prefix=${value#*/}
  address_int=$(ipv4_to_int "$address")
  mask=$(((4294967295 << (32 - prefix)) & 4294967295))
  network_int=$((address_int & mask))
  printf '%s/%s\n' "$(int_to_ipv4 "$network_int")" "$prefix"
}

read_state_value() {
  key=$1
  [ -f "$STATE_FILE" ] || return 0
  sed -n "s/^${key}=//p" "$STATE_FILE" | tail -n 1
}

detect_lan_cidr() {
  if [ -n "${KEKEIO_LAN_CIDR:-}" ]; then
    printf '%s\n' "$KEKEIO_LAN_CIDR"
    return
  fi

  for interface in br-lan br0 lan; do
    candidate=$(ip -o -4 addr show dev "$interface" scope global 2>/dev/null | awk 'NR == 1 { print $4 }')
    if [ -z "$candidate" ]; then
      candidate=$(ip -4 addr show dev "$interface" 2>/dev/null | awk '/inet / { print $2; exit }')
    fi
    if is_ipv4_cidr "$candidate"; then
      printf '%s\n' "$candidate"
      return
    fi
  done

  previous=$(read_state_value KEKEIO_LAN_CIDR)
  if is_ipv4_cidr "$previous"; then
    printf '%s\n' "$previous"
    return
  fi

  [ -r /dev/tty ] || die "无法自动识别 LAN 地址，请以 KEKEIO_LAN_CIDR=路由器IP/前缀 重新运行"
  printf '%s' '请输入路由器 LAN 地址/前缀（例如 192.168.31.1/24）：' >/dev/tty
  IFS= read -r candidate </dev/tty
  is_ipv4_cidr "$candidate" || die "LAN 地址格式无效：$candidate"
  printf '%s\n' "$candidate"
}

generate_origin_host() {
  previous=$(read_state_value KEKEIO_TUNNEL_ORIGIN_HOST)
  if [ -n "$previous" ]; then
    printf '%s\n' "$previous"
    return
  fi
  random_hex=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
  [ "${#random_hex}" -eq 64 ] || die "无法生成源站随机值"
  first=$(printf '%s' "$random_hex" | cut -c1-32)
  second=$(printf '%s' "$random_hex" | cut -c33-64)
  unset random_hex
  printf 'origin-%s.%s.invalid\n' "$first" "$second"
}

write_token_if_missing() {
  if [ -s "$TOKEN_FILE" ]; then
    log "复用现有的受保护 Tunnel Token 文件"
  else
    [ -r /dev/tty ] || die "首次安装必须在交互式终端中安全输入 Tunnel Token"
    tty_state=$(stty -g </dev/tty) || die "无法读取终端状态"
    restore_tty() {
      stty "$tty_state" </dev/tty >/dev/null 2>&1 || true
    }
    trap 'restore_tty; exit 130' HUP INT TERM
    printf '%s' '粘贴新的 Cloudflare Tunnel Token（输入不会显示）：' >/dev/tty
    stty -echo </dev/tty
    IFS= read -r tunnel_token </dev/tty || {
      restore_tty
      die "读取 Tunnel Token 失败"
    }
    restore_tty
    trap - HUP INT TERM
    printf '\n' >/dev/tty
    [ "${#tunnel_token}" -ge 80 ] || die "Tunnel Token 长度异常，请确认只粘贴 Token 本身"
    printf '%s' "$tunnel_token" >"$TOKEN_FILE"
    unset tunnel_token
  fi

  chown 65532:65532 "$TOKEN_FILE" || die "无法设置 Tunnel Token 文件属主"
  chmod 400 "$TOKEN_FILE" || die "无法保护 Tunnel Token 文件"
}

verify_image() {
  image=$1
  platform=$(docker image inspect "$image" --format '{{.Os}}/{{.Architecture}}' 2>/dev/null || true)
  [ "$platform" = "linux/arm64" ] || die "镜像 $image 不是 linux/arm64（实际：${platform:-不存在}）"
}

ensure_network() {
  if docker network inspect "$EDGE_NETWORK" >/dev/null 2>&1; then
    current_subnet=$(docker network inspect "$EDGE_NETWORK" --format '{{(index .IPAM.Config 0).Subnet}}')
    [ "$current_subnet" = "$DOCKER_SUBNET" ] || die "现有网络 $EDGE_NETWORK 使用 $current_subnet，不是预期的 $DOCKER_SUBNET"
  else
    docker network create --driver bridge --subnet "$DOCKER_SUBNET" "$EDGE_NETWORK" >/dev/null || \
      die "无法创建 Docker 网络 $DOCKER_SUBNET；它可能与现有 LAN/VPN/Docker 网段冲突"
  fi
}

assert_replaceable_container() {
  name=$1
  docker container inspect "$name" >/dev/null 2>&1 || return 0
  owner=$(docker container inspect "$name" --format "{{index .Config.Labels \"${MANAGED_LABEL}\"}}" 2>/dev/null || true)
  compose_project=$(docker container inspect "$name" --format '{{index .Config.Labels "com.docker.compose.project"}}' 2>/dev/null || true)
  if [ "$owner" != "$MANAGED_VALUE" ] && [ "$compose_project" != "kekeio-tab" ] && [ "$REPLACE_UNMANAGED" -ne 1 ]; then
    die "容器 $name 已存在且不由本安装器管理；确认无需保留后，用 --replace 重新运行"
  fi
}

remove_existing_container() {
  name=$1
  docker container inspect "$name" >/dev/null 2>&1 || return 0
  log "替换旧容器：$name（持久数据和 Caddy 卷不会删除）"
  docker rm -f "$name" >/dev/null
}

wait_for_healthy() {
  name=$1
  attempts=${2:-60}
  count=0
  while [ "$count" -lt "$attempts" ]; do
    health=$(docker container inspect "$name" --format '{{.State.Health.Status}}' 2>/dev/null || true)
    case "$health" in
      healthy) return 0 ;;
      unhealthy)
        docker logs --tail 80 "$name" >&2 || true
        die "容器 $name 健康检查失败"
        ;;
    esac
    count=$((count + 1))
    sleep 2
  done
  docker logs --tail 80 "$name" >&2 || true
  die "等待容器 $name 就绪超时"
}

wait_for_cloudflared_ready() {
  attempts=${1:-60}
  count=0
  while [ "$count" -lt "$attempts" ]; do
    if docker exec cloudflared-tab cloudflared tunnel --metrics 127.0.0.1:20241 ready >/dev/null 2>&1; then
      return 0
    fi
    running=$(docker container inspect cloudflared-tab --format '{{.State.Running}}' 2>/dev/null || true)
    if [ "$running" != "true" ]; then
      docker logs --tail 80 cloudflared-tab >&2 || true
      die "Cloudflare Tunnel 容器已停止，请检查 Token 和路由器出站网络"
    fi
    count=$((count + 1))
    sleep 2
  done
  docker logs --tail 80 cloudflared-tab >&2 || true
  die "Cloudflare Tunnel 未能连接；请检查新 Token、系统时间、DNS 与出站 7844/443"
}

cleanup_failed_deployment() {
  status=$?
  if [ "$status" -ne 0 ] && [ "$DEPLOYMENT_COMPLETE" -ne 1 ]; then
    for name in $CREATED_CONTAINERS; do
      owner=$(docker container inspect "$name" --format "{{index .Config.Labels \"${MANAGED_LABEL}\"}}" 2>/dev/null || true)
      if [ "$owner" = "$MANAGED_VALUE" ]; then
        docker rm -f "$name" >/dev/null 2>&1 || true
      fi
    done
  fi
}

self_test() {
  is_ipv4_cidr 192.168.31.1/24 || die "CIDR 自检失败"
  [ "$(cidr_network 192.168.31.1/24)" = "192.168.31.0/24" ] || die "网段计算自检失败"
  ! is_ipv4_cidr 999.168.31.1/24 || die "非法 IPv4 自检失败"
  random_hex=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
  [ "${#random_hex}" -eq 64 ] || die "随机数自检失败"
  printf '%s\n' "一键安装器自检通过"
}

usage() {
  cat <<'EOF'
用法：
  sh install.sh             首次安装或更新由本脚本管理的容器
  sh install.sh --replace   明确替换同名的旧版/手工容器
  sh install.sh --self-test 仅运行脚本自检

可选环境变量：
  KEKEIO_LAN_CIDR=192.168.31.1/24
  KEKEIO_RUNTIME_DIR=/mnt/usb-xxxx/mi_docker/tab
EOF
}

case "${1:-}" in
  '') ;;
  --replace) REPLACE_UNMANAGED=1 ;;
  --self-test) self_test; exit 0 ;;
  -h|--help) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac

need_command docker
need_command ip
need_command awk
need_command sed
need_command od
need_command sha256sum

case "$(uname -m)" in
  aarch64|arm64) ;;
  *) die "此发布包只支持 ARM64 路由器（当前：$(uname -m)）" ;;
esac

[ -r "$IMAGE_ARCHIVE" ] || die "找不到离线镜像：$IMAGE_ARCHIVE"
[ -r "$CADDYFILE" ] || die "找不到 Caddy 配置：$CADDYFILE"
docker info >/dev/null 2>&1 || die "Docker 未运行或当前终端没有 Docker 权限"

mkdir -p "$DATA_DIR" "$BACKUP_DIR" "$SECRETS_DIR"
chown -R 10001:10001 "$DATA_DIR" "$BACKUP_DIR"
chmod 700 "$DATA_DIR" "$BACKUP_DIR" "$SECRETS_DIR"

if [ -r "$IMAGE_CHECKSUM" ]; then
  log "校验离线镜像"
  (cd "$(dirname "$IMAGE_ARCHIVE")" && sha256sum -c "$(basename "$IMAGE_CHECKSUM")")
fi

log "加载 GitHub Release 中的 ARM64 镜像"
docker load -i "$IMAGE_ARCHIVE" >/dev/null
verify_image "$APP_IMAGE"
verify_image "$CADDY_IMAGE"
verify_image "$CLOUDFLARED_IMAGE"

LAN_CIDR=$(detect_lan_cidr)
is_ipv4_cidr "$LAN_CIDR" || die "LAN 地址格式无效：$LAN_CIDR"
LAN_IP=${LAN_CIDR%/*}
LAN_NETWORK=$(cidr_network "$LAN_CIDR")
BRIDGE_GATEWAY=$(docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}')
is_ipv4 "$BRIDGE_GATEWAY" || die "无法读取 Docker 默认 bridge 网关"
ORIGIN_HOST=$(generate_origin_host)

write_token_if_missing
ensure_network
docker volume create kekeio-tab-caddy-data >/dev/null
docker volume create kekeio-tab-caddy-config >/dev/null

cat >"$STATE_FILE" <<EOF
KEKEIO_LAN_CIDR=$LAN_CIDR
KEKEIO_LAN_NETWORK=$LAN_NETWORK
KEKEIO_TUNNEL_ORIGIN_BIND=$BRIDGE_GATEWAY
KEKEIO_TUNNEL_ORIGIN_PORT=$ORIGIN_PORT
KEKEIO_TUNNEL_ORIGIN_HOST=$ORIGIN_HOST
EOF
chmod 600 "$STATE_FILE"

for existing_name in cloudflared-tab kekeio-tab-caddy kekeio-tab-backend kekeio-tab; do
  assert_replaceable_container "$existing_name"
done
remove_existing_container cloudflared-tab
remove_existing_container kekeio-tab-caddy
remove_existing_container kekeio-tab-backend
remove_existing_container kekeio-tab

trap cleanup_failed_deployment EXIT
trap 'exit 130' HUP INT TERM

log "启动后端"
docker run -d \
  --name kekeio-tab-backend \
  --label "${MANAGED_LABEL}=${MANAGED_VALUE}" \
  --restart unless-stopped \
  --stop-timeout 30 \
  --network "$EDGE_NETWORK" \
  --network-alias backend \
  --ip "$BACKEND_IP" \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --security-opt no-new-privileges:true \
  --cap-drop ALL \
  --pids-limit 128 \
  --log-driver json-file \
  --log-opt max-size=10m \
  --log-opt max-file=3 \
  -e FULLPRO_ADDR=:9009 \
  -e FULLPRO_DB=/data/fullpro.db \
  -e FULLPRO_BACKUP_DIRECTORY=/backups \
  -e FULLPRO_COOKIE_SECURE=true \
  -e "FULLPRO_PUBLIC_BASE_URL=https://${DOMAIN}" \
  -e FULLPRO_HEALTHCHECK_URL=http://127.0.0.1:9009/health/live \
  -e "FULLPRO_ADMIN_ALLOWED_CIDRS=127.0.0.1/32,::1/128,${LAN_NETWORK}" \
  -e "FULLPRO_TRUSTED_PROXIES=${CADDY_IP}/32" \
  -v "${DATA_DIR}:/data" \
  -v "${BACKUP_DIR}:/backups" \
  "$APP_IMAGE" >/dev/null
CREATED_CONTAINERS="kekeio-tab-backend $CREATED_CONTAINERS"
wait_for_healthy kekeio-tab-backend

log "启动 Caddy 公网白名单与局域网管理入口"
docker run -d \
  --name kekeio-tab-caddy \
  --label "${MANAGED_LABEL}=${MANAGED_VALUE}" \
  --restart unless-stopped \
  --stop-timeout 30 \
  --network "$EDGE_NETWORK" \
  --ip "$CADDY_IP" \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=32m \
  --security-opt no-new-privileges:true \
  --cap-drop ALL \
  --cap-add NET_BIND_SERVICE \
  --pids-limit 128 \
  --log-driver json-file \
  --log-opt max-size=10m \
  --log-opt max-file=3 \
  --health-cmd 'wget -q -T 2 -O /dev/null http://127.0.0.1:8081/health/live || exit 1' \
  --health-interval 30s \
  --health-timeout 3s \
  --health-start-period 30s \
  --health-retries 3 \
  -p "${LAN_IP}:8443:443/tcp" \
  -p "${LAN_IP}:8443:443/udp" \
  -p "${BRIDGE_GATEWAY}:${ORIGIN_PORT}:8081/tcp" \
  -e "KEKEIO_DOMAIN=${DOMAIN}" \
  -e "KEKEIO_ADMIN_HOST=${LAN_IP}" \
  -e "KEKEIO_ADMIN_NETWORKS=${LAN_NETWORK}" \
  -e "KEKEIO_TUNNEL_ORIGIN_HOST=${ORIGIN_HOST}" \
  -v "${CADDYFILE}:/etc/caddy/Caddyfile:ro" \
  -v kekeio-tab-caddy-data:/data \
  -v kekeio-tab-caddy-config:/config \
  "$CADDY_IMAGE" >/dev/null
CREATED_CONTAINERS="kekeio-tab-caddy $CREATED_CONTAINERS"
wait_for_healthy kekeio-tab-caddy

log "启动 Cloudflare Tunnel"
docker run -d \
  --name cloudflared-tab \
  --label "${MANAGED_LABEL}=${MANAGED_VALUE}" \
  --restart unless-stopped \
  --stop-timeout 30 \
  --network bridge \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=16m \
  --security-opt no-new-privileges:true \
  --cap-drop ALL \
  --pids-limit 128 \
  --log-driver json-file \
  --log-opt max-size=10m \
  --log-opt max-file=3 \
  -v "${TOKEN_FILE}:/run/secrets/cloudflare-tunnel-token:ro" \
  "$CLOUDFLARED_IMAGE" \
  tunnel --no-autoupdate --metrics 0.0.0.0:20241 run \
  --token-file /run/secrets/cloudflare-tunnel-token >/dev/null
CREATED_CONTAINERS="cloudflared-tab $CREATED_CONTAINERS"
wait_for_cloudflared_ready

docker cp kekeio-tab-caddy:/data/caddy/pki/authorities/local/root.crt \
  "${RUNTIME_DIR}/kekeio-tab-local-root.crt" >/dev/null 2>&1 || true
if [ -f "${RUNTIME_DIR}/kekeio-tab-local-root.crt" ]; then
  chmod 644 "${RUNTIME_DIR}/kekeio-tab-local-root.crt"
fi

cat >"$SETTINGS_FILE" <<EOF
Cloudflare Tunnel Published application
Hostname: ${DOMAIN}
Service URL: http://${BRIDGE_GATEWAY}:${ORIGIN_PORT}
HTTP Host Header: ${ORIGIN_HOST}

局域网首次安装：https://${LAN_IP}:8443/install
局域网后台：https://${LAN_IP}:8443/admin
EOF
chmod 600 "$SETTINGS_FILE"

DEPLOYMENT_COMPLETE=1
trap - EXIT HUP INT TERM

log "部署完成"
printf '\n%s\n' "Cloudflare 页面只需填写："
printf '  Hostname: %s\n' "$DOMAIN"
printf '  Service URL: http://%s:%s\n' "$BRIDGE_GATEWAY" "$ORIGIN_PORT"
printf '  HTTP Host Header: %s\n' "$ORIGIN_HOST"
printf '\n局域网首次安装：%s\n' "https://${LAN_IP}:8443/install"
printf '配置副本：%s\n' "$SETTINGS_FILE"
if [ -f "${RUNTIME_DIR}/kekeio-tab-local-root.crt" ]; then
  printf 'Caddy 根证书：%s\n' "${RUNTIME_DIR}/kekeio-tab-local-root.crt"
fi
