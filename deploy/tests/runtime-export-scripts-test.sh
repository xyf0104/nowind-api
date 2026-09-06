#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
EXPORT_SCRIPT="$ROOT/xiass-runtime-export.sh"
RESTORE_SCRIPT="$ROOT/xiass-runtime-restore.sh"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
die() { fail "$@"; }
log() { :; }
sleep() { :; }

load_restore_function() {
    sed -n "/^$1() {$/,/^}$/p" "$RESTORE_SCRIPT" > "$fixture/$1.sh"
    source "$fixture/$1.sh"
}

run_bootstrap_fixture() {
    local scenario="$1" fixture="$2" fixture_manager="$3" installed=false
    local PACKAGE_DIR="$fixture/package" COMPOSE=()
    mkdir -p "$PACKAGE_DIR/payload/app-data" "$PACKAGE_DIR/deploy"
    touch "$PACKAGE_DIR/manifest.json" "$PACKAGE_DIR/checksums.sha256" \
        "$PACKAGE_DIR/payload/postgres.sql.gz" "$PACKAGE_DIR/payload/redis.rdb" \
        "$PACKAGE_DIR/payload/.env" "$PACKAGE_DIR/deploy/docker-compose.local.yml"
    cp "$RESTORE_SCRIPT" "$PACKAGE_DIR/restore-xiass.sh"
    chmod +x "$PACKAGE_DIR/restore-xiass.sh"
    printf '{}\n' > "$PACKAGE_DIR/payload/runtime-context.json"
    printf 'user default on nopass ~* +@all\n' > "$PACKAGE_DIR/payload/redis.acl"
    command() {
        [ "$1" = -v ] || fail "unexpected command invocation: $*"
        case "$2" in
            jq|sha256sum)
                [ "$installed" = true ] && [ "$scenario" != missing-after-install ] ;;
            apt-get|dnf|yum|apk) [ "$2" = "$fixture_manager" ] ;;
            docker|curl|wget|tar|gzip) return 0 ;;
            systemctl|docker-compose) return 1 ;;
            *) fail "unexpected tool lookup: $*" ;;
        esac
    }
    install_fixture_packages() {
        [ "$*" != update ] || return 0
        case " $* " in *' jq '*) ;; *) fail 'bootstrap omitted jq' ;; esac
        case " $* " in *' coreutils '*) ;; *) fail 'bootstrap omitted sha256sum provider' ;; esac
        installed=true
        printf 'installed\n' >> "$fixture/calls"
    }
    apt-get() { install_fixture_packages "$@"; }
    dnf() { install_fixture_packages "$@"; }
    yum() { install_fixture_packages "$@"; }
    apk() { install_fixture_packages "$@"; }
    docker() {
        case "$*" in
            info|'compose version') [ "$installed" = true ] ;;
            *) fail "unexpected Docker operation: $*" ;;
        esac
    }
    sha256sum() {
        [ "$installed" = true ] || fail 'checksum validation ran before bootstrap'
        [ "$*" = '-c checksums.sha256' ] || fail 'package checksum validation was changed'
        printf 'checksums\n' >> "$fixture/calls"
        [ "$scenario" != bad-checksum ]
    }
    jq() {
        [ "$installed" = true ] || fail 'manifest validation ran before bootstrap'
        case "$*" in
            '-r --arg key format_version '*)
                printf 'manifest\n' >> "$fixture/calls"
                if [ "$scenario" = bad-format ]; then printf '99\n'; else printf '2\n'; fi ;;
            '-e '*)
                printf 'context\n' >> "$fixture/calls"
                [ "$scenario" != bad-context ] ;;
            *) fail "unexpected JSON operation: $*" ;;
        esac
    }
    # Execute the real preflight order, stopping before any installation data changes.
    sed -n '/^install_packages() {/,/^SOURCE_DOMAIN=/ { /^SOURCE_DOMAIN=/q; p; }' "$RESTORE_SCRIPT" > "$fixture/preflight.sh"
    source "$fixture/preflight.sh"
    printf 'validated\n' >> "$fixture/calls"
}

run_redis_fixture() {
    local scenario="$1" fixture="$2" restarted=false
    local PACKAGE_DIR="$fixture/package" DEPLOY_DIR="$fixture/deploy"
    local RESTORE_REDIS_USER=xiass-restore-fixture RESTORE_REDIS_PASSWORD=fixture-password
    mkdir -p "$PACKAGE_DIR/payload" "$DEPLOY_DIR/redis_data"
    printf 'user default on nopass ~* +@all\n' > "$PACKAGE_DIR/payload/redis.acl"
    cp "$PACKAGE_DIR/payload/redis.acl" "$DEPLOY_DIR/redis_data/users.acl"
    printf 'appendonly no\n' > "$DEPLOY_DIR/redis_data/redis.conf"
    load_restore_function finish_redis_restore
    sed() {
        if [ "$(uname)" = Darwin ] && [ "$1" = -i ]; then
            shift
            command sed -i '' "$@"
        else command sed "$@"; fi
    }
    chown() {
        [ "$*" = "--reference=$DEPLOY_DIR/redis_data/users.acl $DEPLOY_DIR/redis_data/redis.conf" ] || fail 'unexpected ownership change'
    }
    compose() {
        case "$*" in
            'restart redis')
                cmp -s "$PACKAGE_DIR/payload/redis.acl" "$DEPLOY_DIR/redis_data/users.acl" || fail 'original Redis ACL was not restored'
                grep -qx 'appendonly yes' "$DEPLOY_DIR/redis_data/redis.conf" || fail 'Redis persistence was not saved'
                restarted=true ;;
            *'unset REDISCLI_AUTH; exec redis-cli -e --raw -x AUTH "$1"'*)
                [ "$*" = 'exec -T redis sh -c unset REDISCLI_AUTH; exec redis-cli -e --raw -x AUTH "$1" sh xiass-restore-fixture' ] || fail 'unexpected AUTH command'
                [ "$restarted" = true ] || fail 'AUTH rejection checked before restart'
                [ "$(cat)" = "$RESTORE_REDIS_PASSWORD" ] || fail 'AUTH did not receive the temporary password on stdin'
                printf 'auth\n' >> "$fixture/calls"
                case "$scenario" in
                    removed-nopass|removed-password)
                        printf 'WRONGPASS invalid username-password pair or user is disabled.\n'; return 1 ;;
                    retained) printf 'OK\n' ;;
                    transport-failure) printf 'Could not connect to Redis\n' >&2; return 1 ;;
                    denied-command) printf 'NOPERM no permissions to run AUTH\n'; return 1 ;;
                    misleading-success) printf 'WRONGPASS invalid username-password pair\n' ;;
                esac ;;
            *'--raw INFO persistence')
                printf 'aof_enabled:1\naof_rewrite_in_progress:0\naof_last_bgrewrite_status:ok\n' ;;
            'exec -T redis redis-check-rdb /data/dump.rdb'|*'CONFIG SET appendonly yes'|*'ACL LOAD') return 0 ;;
            *PING*)
                # redis-cli can report PONG as default/nopass after its automatic AUTH failed.
                if [ "$restarted" = true ] && [[ "$*" == *"--user $RESTORE_REDIS_USER"* ]]; then
                    printf 'fallback-ping\n' >> "$fixture/calls"
                    [ "$scenario" != removed-password ] || return 1
                fi
                printf 'PONG\n' ;;
            *) fail "unexpected Redis command: $*" ;;
        esac
    }
    finish_redis_restore
    [ -z "$RESTORE_REDIS_PASSWORD" ] || fail 'temporary password was not cleared'
}

run_health_fixture() {
    local scenario="$1" fixture="$2" fixture_endpoint expected_url attempts=0
    local DEPLOY_DIR="$fixture/deploy" RESTORE_CONFIG="$fixture/deploy/restore.json" COMPOSE=(docker compose)
    mkdir -p "$DEPLOY_DIR"
    printf 'SERVER_PORT="19999"\n' > "$DEPLOY_DIR/.env"
    case "$scenario" in
        wildcard|retry) fixture_endpoint='0.0.0.0:28080'; expected_url='http://127.0.0.1:28080/readyz' ;;
        bound) fixture_endpoint='127.0.0.2:28081'; expected_url='http://127.0.0.2:28081/readyz' ;;
        ipv6) fixture_endpoint='[::]:28082'; expected_url='http://[::1]:28082/readyz' ;;
        ipv6-bound) fixture_endpoint='[::1]:28083'; expected_url='http://[::1]:28083/readyz' ;;
        dual-stack) fixture_endpoint=$'0.0.0.0:28084\n[::]:28084'; expected_url='http://127.0.0.1:28084/readyz' ;;
        unpublished|lookup-failure) fixture_endpoint='' ;;
        invalid) fixture_endpoint='0.0.0.0:not-a-port' ;;
        unhealthy) fixture_endpoint='127.0.0.1:28085'; expected_url='http://127.0.0.1:28085/readyz' ;;
    esac
    load_restore_function compose
    load_restore_function read_env_value
    load_restore_function wait_for_health
    docker() {
        [ "$*" = "compose -f $DEPLOY_DIR/docker-compose.local.yml -f $RESTORE_CONFIG --project-directory $DEPLOY_DIR port xiass-api 8080" ] || fail 'health endpoint lookup lost Compose configuration'
        printf 'port\n' >> "$fixture/calls"
        [ "$scenario" != lookup-failure ] || return 1
        printf '%s\n' "$fixture_endpoint"
    }
    curl() {
        printf 'curl\n' >> "$fixture/calls"
        [ "$*" = "-fsS --max-time 3 $expected_url" ] || fail 'health check ignored the published endpoint'
        attempts=$((attempts + 1))
        [ "$scenario" != unhealthy ] || return 22
        [ "$scenario" != retry ] || [ "$attempts" -ge 2 ]
    }
    wait_for_health
    if [ "$scenario" = retry ]; then
        [ "$attempts" -eq 2 ] || fail 'health retry was lost'
    fi
}

if [ "${1:-}" = --fixture ]; then
    case "$2" in
        bootstrap) run_bootstrap_fixture "$3" "$4" "$5" ;;
        redis) run_redis_fixture "$3" "$4" ;;
        health) run_health_fixture "$3" "$4" ;;
        *) fail "unknown fixture: $2" ;;
    esac
    exit 0
fi

bash -n "$EXPORT_SCRIPT"
bash -n "$RESTORE_SCRIPT"

INSTALL_DIR=/tmp/xiass-runtime-export-test bash "$EXPORT_SCRIPT" --help | grep -q 'xiass-runtime-export.sh'
bash "$RESTORE_SCRIPT" --help | grep -q 'restore-xiass.sh'

# Regression guards for the security and portability invariants of the package.
grep -Fq 'exec sudo -E bash "$0" "${ORIGINAL_ARGS[@]}"' "$RESTORE_SCRIPT"
grep -Fq 'rm -rf "$WORK_DIR/payload/app-data/runtime-exports"' "$EXPORT_SCRIPT"
grep -Fq -- '--publish-to-container' "$EXPORT_SCRIPT"
grep -Fq -- '--postgres-from-container' "$EXPORT_SCRIPT"
grep -Fq 'docker cp "$POSTGRES_FROM_CONTAINER" "$WORK_DIR/payload/postgres.sql.gz"' "$EXPORT_SCRIPT"
! grep -Fq 'POSTGRES_CONTAINER=' "$EXPORT_SCRIPT"
grep -Fq 'PUBLISH_TEMP_PATH="${PUBLISH_PATH}.partial"' "$EXPORT_SCRIPT"
grep -Fq 'docker cp "$OUTPUT" "$PUBLISH_CONTAINER:$PUBLISH_TEMP_PATH"' "$EXPORT_SCRIPT"
grep -Fq 'mv -f "$1" "$2"' "$EXPORT_SCRIPT"
grep -Fq 'compose_at "$existing_deploy" "$compose_file" down --remove-orphans' "$RESTORE_SCRIPT"

mkdir -p "$ROOT/../../artifacts"
TEST_DIR=$(mktemp -d "$ROOT/../../artifacts/runtime-restore-tests.XXXXXX")
trap 'rm -rf "$TEST_DIR"' EXIT
for flow in bootstrap redis health; do
    case "$flow" in
        bootstrap) scenarios=(apt-get dnf yum apk bad-checksum bad-format bad-context missing-after-install) ;;
        redis) scenarios=(removed-nopass removed-password retained transport-failure denied-command misleading-success) ;;
        health) scenarios=(wildcard bound ipv6 ipv6-bound dual-stack retry unpublished lookup-failure invalid unhealthy) ;;
    esac
    for scenario in "${scenarios[@]}"; do
        fixture="$TEST_DIR/$flow-$scenario"
        mkdir -p "$fixture"
        : > "$fixture/calls"
        status=0 expected=0 manager=apt-get
        case "$scenario" in
            dnf|yum|apk) manager="$scenario" ;;
            bad-*|missing-after-install|retained|*-failure|denied-command|misleading-success|unpublished|invalid|unhealthy) expected=1 ;;
        esac
        bash "$0" --fixture "$flow" "$scenario" "$fixture" "$manager" > "$fixture/output" 2>&1 || status=$?
        if [ "$status" -ne "$expected" ]; then
            cat "$fixture/output"
            fail "$flow/$scenario returned $status, expected $expected"
        fi
        case "$flow" in
            bootstrap)
                grep -qx installed "$fixture/calls" || fail 'clean-host bootstrap did not run'
                if [ "$expected" -eq 0 ]; then
                    [ "$(cat "$fixture/calls")" = $'installed\nchecksums\nmanifest\ncontext\nvalidated' ] || fail 'bootstrap bypassed validation or ran out of order'
                else
                    ! grep -qx validated "$fixture/calls" || fail 'invalid package or missing tools passed preflight'
                    case "$scenario" in
                        bad-checksum) ! grep -qx manifest "$fixture/calls" || fail 'failed checksum validation did not stop restore' ;;
                        bad-format) ! grep -qx context "$fixture/calls" || fail 'unsupported format did not stop restore' ;;
                        bad-context) grep -qx context "$fixture/calls" || fail 'runtime context was not validated' ;;
                        missing-after-install) ! grep -qx checksums "$fixture/calls" || fail 'missing tools did not stop restore' ;;
                    esac
                fi ;;
            redis)
                grep -qx auth "$fixture/calls" || fail 'temporary Redis credentials were not explicitly tested'
                ! grep -qx fallback-ping "$fixture/calls" || fail 'temporary Redis identity was checked with PING' ;;
            health)
                grep -qx port "$fixture/calls" || fail 'health check did not query Compose port'
                case "$scenario" in
                    unpublished|lookup-failure|invalid) [ "$(cat "$fixture/calls")" = port ] || fail 'health check fell back to a stale env port' ;;
                    *) grep -qx curl "$fixture/calls" || fail 'published health endpoint was not checked' ;;
                esac ;;
        esac
        printf 'PASS: %s/%s\n' "$flow" "$scenario"
    done
done
printf 'Runtime export/restore script tests passed (no live Docker/network operations).\n'
