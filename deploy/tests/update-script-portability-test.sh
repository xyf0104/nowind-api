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

prefetch_line=$(grep -n '^[[:space:]]*prefetch_target_app_image$' "$repo_root/deploy/xiass-update.sh" | tail -n 1 | cut -d: -f1)
legacy_down_line=$(grep -n '^[[:space:]]*if ! compose down; then$' "$repo_root/deploy/xiass-update.sh" | tail -n 1 | cut -d: -f1)
if [ -z "$prefetch_line" ] || [ -z "$legacy_down_line" ] || [ "$prefetch_line" -ge "$legacy_down_line" ]; then
    echo "main image must be prefetched before the legacy stack is stopped" >&2
    exit 1
fi

grep -Fq 'compose up -d --no-deps --no-build --force-recreate xiass-api' "$repo_root/deploy/xiass-update.sh" \
    || { echo "canonical app hot-swap path is missing" >&2; exit 1; }
grep -Fq 'XIASS_RUNTIME_SKIP_CORE_START="$skip_core_start"' "$repo_root/deploy/xiass-update.sh" \
    || { echo "hot-swap must preserve the running database and cache" >&2; exit 1; }
grep -Fq 'up -d --no-deps --no-build --force-recreate team-child-automation' "$repo_root/deploy/xiass-runtime-start.sh" \
    || { echo "hot-swap must recreate the Team automation sidecar" >&2; exit 1; }

echo "Updater BusyBox compatibility and success-status test passed."
