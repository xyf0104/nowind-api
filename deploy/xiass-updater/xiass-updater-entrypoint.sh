#!/usr/bin/env bash
set -Eeuo pipefail

: "${INSTALL_DIR:?INSTALL_DIR is required}"

export BACKUP_DIR="${BACKUP_DIR:-/root/xiass-backups}"
if [ "${1:-}" = "runtime-export" ]; then
    shift
    exec bash /usr/local/lib/xiass/xiass-runtime-export.sh "$@"
fi
if [ "${1:-}" = "cluster-join" ]; then
    shift
    exec bash /usr/local/lib/xiass/xiass-cluster-join.sh "$@"
fi
if [ "${1:-}" = "cluster-runtime" ]; then
    shift
    exec bash /usr/local/lib/xiass/xiass-cluster-runtime.sh "$@"
fi
exec bash /usr/local/lib/xiass/xiass-update.sh "$@"
