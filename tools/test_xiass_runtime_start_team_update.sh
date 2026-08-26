#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/deploy/xiass-runtime-start.sh"
CALLS="$(mktemp)"

cleanup() {
    rm -f "$CALLS"
}
trap cleanup EXIT

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

XIASS_RUNTIME_START_LIB_ONLY=1 source "$SCRIPT"

TEAM_CHILD_BROWSER_ENABLED=true
BUILD_MODE=image
SKIP_CORE_START=true
CORE_READY_DELAY_SECONDS=5

service_exists() {
    case "$1" in
        team-child-browser|team-child-automation) return 0 ;;
        *) return 1 ;;
    esac
}

profile_compose() {
    printf '%s\n' "$*" >> "$CALLS"
    if [ "$1" = "ps" ]; then
        printf 'automation-container\n'
    fi
    return 0
}

sleep() {
    printf 'sleep %s\n' "$1" >> "$CALLS"
}

wait_for_automation_health() {
    [ "$1" = "automation-container" ] || fail 'unexpected Team automation container id'
}

start_browser_stack

grep -Fqx 'pull team-child-automation' "$CALLS" || fail 'fast update must pull the Team automation image'
grep -Fqx 'sleep 0' "$CALLS" || fail 'fast update must not delay an already healthy browser stack'
grep -Fqx 'up -d --no-build team-child-browser' "$CALLS" || fail 'fast update must keep the Team browser available without recreating it'
grep -Fqx 'up -d --no-deps --no-build --force-recreate team-child-automation' "$CALLS" || fail 'fast update must recreate the Team automation sidecar'
if grep -Fqx 'pull team-child-browser team-child-automation' "$CALLS"; then
    fail 'fast update must not pull or restart Chromium'
fi

printf 'xiass runtime Team sidecar update test passed.\n'
