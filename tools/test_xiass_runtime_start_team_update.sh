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
grep -Fqx 'up -d --no-deps --no-build --pull never --no-recreate team-child-browser' "$CALLS" || fail 'fast update must preserve the browser and never start its dependencies'
grep -Fqx 'up -d --no-deps --no-build --force-recreate team-child-automation' "$CALLS" || fail 'fast update must recreate the Team automation sidecar'
if grep -Fqx 'pull team-child-browser team-child-automation' "$CALLS"; then
    fail 'fast update must not pull or restart Chromium'
fi

printf 'xiass runtime Team sidecar update test passed.\n'

(
    RUNTIME_COMPOSE_FILES=("/fixture/base.yml" "/fixture/node.yml" "/fixture/proxy.yml")
    COMPOSE_FILE="${RUNTIME_COMPOSE_FILES[0]}"
    DEPLOY_DIR=/fixture
    COMPOSE=(capture_compose)
    capture_compose() { printf '%s\n' "$*" >> "$CALLS"; }
    compose config --quiet
    grep -Fqx -- '-f /fixture/base.yml -f /fixture/node.yml -f /fixture/proxy.yml --project-directory /fixture config --quiet' "$CALLS" \
        || fail 'runtime must retain all node and proxy Compose overlays'
    RUNTIME_PROJECT_NAME=paired-primary
    compose config --quiet
    grep -Fqx -- '-f /fixture/base.yml -f /fixture/node.yml -f /fixture/proxy.yml --project-directory /fixture --project-name paired-primary config --quiet' "$CALLS" \
        || fail 'runtime must retain the running Compose project and named-volume identity'
)

(
    service_exists() { return 0; }
    compose() { printf '%s\n' "$*" >> "$CALLS"; }
    wait_for_core_health() { return 0; }
    XIASS_RUNTIME_CORE_READY=true
    start_core
    grep -Fqx 'up -d --no-build --pull never postgres redis watchtower xiass-api' "$CALLS" \
        || fail 'prepared update must not pull while the old stack is stopped'
    XIASS_RUNTIME_CORE_READY=false
    start_core
    grep -Fqx 'up -d --no-build postgres redis watchtower xiass-api' "$CALLS" \
        || fail 'fresh installation must still allow missing dependency images'
)

printf 'xiass runtime overlay and prepared-image tests passed.\n'
