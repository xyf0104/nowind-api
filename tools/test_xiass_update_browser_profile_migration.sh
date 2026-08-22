#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/deploy/xiass-update.sh"
TEST_DIR="$(mktemp -d)"

cleanup() {
    rm -rf "$TEST_DIR"
}
trap cleanup EXIT

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

XIASS_UPDATE_LIB_ONLY=1 source "$SCRIPT"

DEPLOY_DIR="$TEST_DIR/deploy"
legacy_profile="$TEST_DIR/legacy-browser-profile"
target_profile="$DEPLOY_DIR/team_child_browser_data"
mkdir -p "$legacy_profile" "$DEPLOY_DIR"
printf 'persisted-login-state\n' > "$legacy_profile/.profile-state"

TEAM_CHILD_BROWSER_PROFILE_SOURCE="$legacy_profile"
migrate_team_child_browser_profile
cmp -s "$legacy_profile/.profile-state" "$target_profile/.profile-state" \
    || fail 'legacy browser profile was not copied to the canonical directory'

printf 'canonical-profile-state\n' > "$target_profile/.profile-state"
printf 'newer-legacy-state\n' > "$legacy_profile/.profile-state"
migrate_team_child_browser_profile
grep -Fqx 'canonical-profile-state' "$target_profile/.profile-state" \
    || fail 'existing canonical browser profile was overwritten'

TEAM_CHILD_BROWSER_PROFILE_SOURCE="$target_profile"
migrate_team_child_browser_profile
grep -Fqx 'canonical-profile-state' "$target_profile/.profile-state" \
    || fail 'canonical browser profile changed during same-directory migration'

capture_source="$TEST_DIR/captured-volume-profile"
mkdir -p "$capture_source"
printf 'volume-profile-state\n' > "$capture_source/.profile-state"
container_exists() {
    [ "$1" = "xiass-api-team-child-browser" ]
}
docker() {
    if [ "$1" = "inspect" ]; then
        printf 'volume|%s\n' "$capture_source"
        return 0
    fi
    return 1
}
TEAM_CHILD_BROWSER_PROFILE_SOURCE=""
capture_team_child_browser_profile
[ "$TEAM_CHILD_BROWSER_PROFILE_SOURCE" = "$capture_source" ] \
    || fail 'existing named-volume browser profile was not captured'

printf 'xiass update browser profile migration tests passed.\n'
