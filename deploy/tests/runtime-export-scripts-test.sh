#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
EXPORT_SCRIPT="$ROOT/xiass-runtime-export.sh"
RESTORE_SCRIPT="$ROOT/xiass-runtime-restore.sh"

bash -n "$EXPORT_SCRIPT"
bash -n "$RESTORE_SCRIPT"

INSTALL_DIR=/tmp/xiass-runtime-export-test bash "$EXPORT_SCRIPT" --help | grep -q 'xiass-runtime-export.sh'
bash "$RESTORE_SCRIPT" --help | grep -q 'restore-xiass.sh'

# Regression guards for the security and portability invariants of the package.
grep -Fq 'exec sudo -E bash "$0" "${ORIGINAL_ARGS[@]}"' "$RESTORE_SCRIPT"
grep -Fq 'rm -rf "$WORK_DIR/payload/app-data/runtime-exports"' "$EXPORT_SCRIPT"
grep -Fq -- '--publish-to-container' "$EXPORT_SCRIPT"
grep -Fq 'PUBLISH_TEMP_PATH="${PUBLISH_PATH}.partial"' "$EXPORT_SCRIPT"
grep -Fq 'docker cp "$OUTPUT" "$PUBLISH_CONTAINER:$PUBLISH_TEMP_PATH"' "$EXPORT_SCRIPT"
grep -Fq 'mv -f "$1" "$2"' "$EXPORT_SCRIPT"
grep -Fq 'compose_at "$existing_deploy" "$compose_file" down --remove-orphans' "$RESTORE_SCRIPT"
