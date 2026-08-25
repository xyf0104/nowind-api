#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)

docker run --rm \
    -v "$repo_root:/repo:ro" \
    alpine:3.20 \
    sh -eu -c '
        apk add --no-cache bash >/dev/null
        mkdir -p /tmp/backups
        touch /tmp/backups/xiass-runtime-1.tar.gz /tmp/backups/xiass-runtime-1.tar.gz.sha256
        sleep 1
        touch /tmp/backups/xiass-runtime-2.tar.gz /tmp/backups/xiass-runtime-2.tar.gz.sha256
        sleep 1
        touch /tmp/backups/xiass-runtime-3.tar.gz /tmp/backups/xiass-runtime-3.tar.gz.sha256
        BACKUP_DIR=/tmp/backups KEEP_BACKUPS=2 XIASS_BACKUP_LIB_ONLY=1 \
            bash -c '\''source /repo/deploy/xiass-backup.sh; prune_old_backups'\''
        test ! -e /tmp/backups/xiass-runtime-1.tar.gz
        test ! -e /tmp/backups/xiass-runtime-1.tar.gz.sha256
        test -e /tmp/backups/xiass-runtime-2.tar.gz
        test -e /tmp/backups/xiass-runtime-3.tar.gz
        test "$(find /tmp/backups -name "xiass-runtime-*.tar.gz" | wc -l)" -eq 2
    '

if grep -Fq '[ -n "$patch_file" ] && printf' "$repo_root/deploy/xiass-update.sh"; then
    echo "update success path can still return a false conditional status" >&2
    exit 1
fi

echo "Updater BusyBox compatibility and success-status test passed."
