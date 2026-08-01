#!/bin/sh
set -eu

# Docker 会以 root 创建首次出现的 bind mount 源目录。这里只修正两个专用
# 挂载点本身，随后立即降权；不会递归修改宿主机中的其他路径。
if [ "$(id -u)" = "0" ]; then
    mkdir -p /data /backups
    chown 10001:10001 /data /backups

    su-exec 10001:10001 sh -c '
        data_probe=/data/.kekeio-write-test.$$
        backup_probe=/backups/.kekeio-write-test.$$
        cleanup() {
            rm -f "$data_probe" "$backup_probe"
        }
        trap cleanup EXIT HUP INT TERM
        : > "$data_probe"
        : > "$backup_probe"
    '

    exec su-exec 10001:10001 "$@"
fi

exec "$@"
