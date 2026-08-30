#!/usr/bin/env bash
# Build an online XIASS migration package from a running Docker deployment.
# This script runs only inside the short-lived XIASS updater sidecar. The
# sidecar gets a read-only view of the host installation plus Docker-socket
# access. It publishes only the finished archive into protected application
# data through Docker's scoped file-copy operation.

set -Eeuo pipefail
umask 077

: "${INSTALL_DIR:?INSTALL_DIR is required}"

OUTPUT=""
PUBLISH_TO_CONTAINER=""
PUBLISH_CONTAINER=""
PUBLISH_PATH=""
PUBLISH_TEMP_PATH=""
PUBLISH_COMPLETED=false
SOURCE_ORIGIN="${XIASS_SOURCE_ORIGIN:-}"
APP_CONTAINER=""
POSTGRES_CONTAINER=""
REDIS_CONTAINER=""
TEAM_BROWSER_CONTAINER=""
TEAM_AUTOMATION_CONTAINER=""
WORK_DIR=""

log() { printf '[XIASS] %s\n' "$*"; }
die() { printf '[XIASS] 错误：%s\n' "$*" >&2; exit 1; }

cleanup() {
    local status=$?
    if [ -n "$PUBLISH_CONTAINER" ] && [ -n "$PUBLISH_TEMP_PATH" ] && [ "$PUBLISH_COMPLETED" != "true" ] && command -v docker >/dev/null 2>&1; then
        docker exec "$PUBLISH_CONTAINER" rm -f "$PUBLISH_TEMP_PATH" >/dev/null 2>&1 || true
    fi
    [ -z "$WORK_DIR" ] || rm -rf "$WORK_DIR"
    exit "$status"
}

usage() {
    cat <<'EOF'
Usage: xiass-runtime-export.sh --output /tmp/xiass-migration.tar.gz
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --output)
            [ "$#" -ge 2 ] || die "--output requires a path"
            OUTPUT="$2"
            shift 2
            ;;
        --publish-to-container)
            [ "$#" -ge 2 ] || die "--publish-to-container requires a target"
            PUBLISH_TO_CONTAINER="$2"
            shift 2
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            die "unknown argument: $1"
            ;;
    esac
done

[ -n "$OUTPUT" ] || die "--output is required"
case "$OUTPUT" in
    /*) ;;
    *) die "output path must be absolute" ;;
esac
if [ -n "$PUBLISH_TO_CONTAINER" ]; then
    case "$PUBLISH_TO_CONTAINER" in
        *:* )
            PUBLISH_CONTAINER="${PUBLISH_TO_CONTAINER%%:*}"
            PUBLISH_PATH="${PUBLISH_TO_CONTAINER#*:}"
            [ -n "$PUBLISH_CONTAINER" ] && [ -n "$PUBLISH_PATH" ] || die "publish target must include a container and absolute path"
            case "$PUBLISH_PATH" in
                /*) ;;
                *) die "publish target path must be absolute" ;;
            esac
            PUBLISH_TEMP_PATH="${PUBLISH_PATH}.partial"
            ;;
        * ) die "publish target must use container:path format" ;;
    esac
fi

command -v docker >/dev/null 2>&1 || die "Docker CLI is unavailable"
command -v tar >/dev/null 2>&1 || die "tar is unavailable"
command -v gzip >/dev/null 2>&1 || die "gzip is unavailable"
command -v jq >/dev/null 2>&1 || die "jq is unavailable"
[ -S /var/run/docker.sock ] || die "Docker socket is unavailable"
[ -f "$INSTALL_DIR/deploy/.env" ] || die "missing deployment .env"
[ -f "$INSTALL_DIR/deploy/docker-compose.local.yml" ] || die "missing canonical docker-compose.local.yml"

container_is_running() {
    [ "$(docker inspect --type container --format '{{.State.Running}}' "$1" 2>/dev/null || true)" = "true" ]
}

container_exists() {
    docker inspect --type container "$1" >/dev/null 2>&1
}

find_container() {
    local candidate
    for candidate in "$@"; do
        if container_is_running "$candidate"; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done
    return 1
}

APP_CONTAINER=$(find_container xiass-api nowind-api sub2api) || die "XIASS application container is not running"
POSTGRES_CONTAINER=$(find_container xiass-api-postgres nowind-api-postgres sub2api-postgres) || die "PostgreSQL container is not running"
REDIS_CONTAINER=$(find_container xiass-api-redis nowind-api-redis sub2api-redis) || die "Redis container is not running"
TEAM_BROWSER_CONTAINER=$(find_container xiass-api-team-child-browser nowind-api-team-child-browser sub2api-team-child-browser || true)
TEAM_AUTOMATION_CONTAINER=$(find_container xiass-api-team-child-automation nowind-api-team-child-automation sub2api-team-child-automation || true)

# Browser and automation containers are intentionally optional. Their named or
# bind-mounted profile data is still readable through docker cp while stopped,
# so retain it whenever the containers exist rather than only when they happen
# to be running at the moment a migration package is requested.
if [ -z "$TEAM_BROWSER_CONTAINER" ]; then
    for candidate in xiass-api-team-child-browser nowind-api-team-child-browser sub2api-team-child-browser; do
        if container_exists "$candidate"; then
            TEAM_BROWSER_CONTAINER="$candidate"
            break
        fi
    done
fi
if [ -z "$TEAM_AUTOMATION_CONTAINER" ]; then
    for candidate in xiass-api-team-child-automation nowind-api-team-child-automation sub2api-team-child-automation; do
        if container_exists "$candidate"; then
            TEAM_AUTOMATION_CONTAINER="$candidate"
            break
        fi
    done
fi

WORK_DIR=$(mktemp -d)
trap cleanup EXIT INT TERM
mkdir -p "$WORK_DIR/payload/app-data" "$WORK_DIR/deploy"

log "exporting PostgreSQL logical snapshot"
docker exec "$POSTGRES_CONTAINER" sh -c '
    export PGPASSWORD="${POSTGRES_PASSWORD:-}"
    exec pg_dump -h 127.0.0.1 -U "${POSTGRES_USER:-sub2api}" -d "${POSTGRES_DB:-sub2api}" \
        --no-owner --no-acl --clean --if-exists
' > "$WORK_DIR/payload/postgres.sql"
gzip -9 -n "$WORK_DIR/payload/postgres.sql"

log "exporting Redis RDB snapshot"
redis_snapshot="/tmp/xiass-runtime-export-$$.rdb"
docker exec "$REDIS_CONTAINER" sh -c "redis-cli --rdb '$redis_snapshot' >/dev/null"
docker cp "$REDIS_CONTAINER:$redis_snapshot" "$WORK_DIR/payload/redis.rdb"
docker exec "$REDIS_CONTAINER" rm -f "$redis_snapshot" >/dev/null 2>&1 || true

log "exporting XIASS application data"
docker cp "$APP_CONTAINER:/app/data/." "$WORK_DIR/payload/app-data"
# The migration packages themselves live in /app/data so that the API can
# serve them after the short-lived exporter exits. They must not recursively
# include older archives or the package currently being assembled.
rm -rf "$WORK_DIR/payload/app-data/runtime-exports"

if [ -n "$TEAM_BROWSER_CONTAINER" ]; then
    log "exporting Team browser profile"
    mkdir -p "$WORK_DIR/payload/team-child-browser-data"
    docker cp "$TEAM_BROWSER_CONTAINER:/config/." "$WORK_DIR/payload/team-child-browser-data"
fi

if [ -n "$TEAM_AUTOMATION_CONTAINER" ]; then
    log "exporting Team automation data"
    mkdir -p "$WORK_DIR/payload/team-child-automation-data"
    docker cp "$TEAM_AUTOMATION_CONTAINER:/app/data/." "$WORK_DIR/payload/team-child-automation-data"
fi

log "copying deployment configuration"
cp "$INSTALL_DIR/deploy/.env" "$WORK_DIR/payload/.env"
chmod 600 "$WORK_DIR/payload/.env"
for compose_file in docker-compose.local.yml docker-compose.yml docker-compose.xiass.yml docker-compose.build.yml; do
    if [ -f "$INSTALL_DIR/deploy/$compose_file" ]; then
        cp "$INSTALL_DIR/deploy/$compose_file" "$WORK_DIR/deploy/$compose_file"
    fi
done
cp /usr/local/lib/xiass/xiass-runtime-restore.sh "$WORK_DIR/restore-xiass.sh"
chmod 700 "$WORK_DIR/restore-xiass.sh"

APP_IMAGE=$(docker inspect --format '{{.Config.Image}}' "$APP_CONTAINER" 2>/dev/null || true)
CREATED_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
jq -n \
    --arg created_at "$CREATED_AT" \
    --arg source_origin "$SOURCE_ORIGIN" \
    --arg app_image "$APP_IMAGE" \
    '{format_version: 1, created_at: $created_at, source_origin: $source_origin, app_image: $app_image, layout: "logical"}' \
    > "$WORK_DIR/manifest.json"

cat > "$WORK_DIR/README.txt" <<'EOF'
XIASS API migration package

1. Extract this archive on the target Linux server.
2. Run: sudo ./restore-xiass.sh
3. To replace the original domain during restore, run:
   sudo ./restore-xiass.sh --domain new.example.com

This package contains secrets, account credentials, database data, and browser
profiles. Store it only in a trusted location and delete it after the migration
has been verified.
EOF

(
    cd "$WORK_DIR"
    find . -type f ! -name checksums.sha256 -print0 | sort -z | xargs -0 sha256sum > checksums.sha256
)

mkdir -p "$(dirname "$OUTPUT")"
TEMP_OUTPUT="${OUTPUT}.partial"
rm -f "$TEMP_OUTPUT"
tar -C "$WORK_DIR" -czf "$TEMP_OUTPUT" .
mv "$TEMP_OUTPUT" "$OUTPUT"
chmod 600 "$OUTPUT"

if [ -n "$PUBLISH_TO_CONTAINER" ]; then
    log "publishing migration package to XIASS application data"
    # A restart can make a new API process reconcile the export record while
    # this sidecar is still copying. Publish through a private temporary name,
    # then rename inside the application container so a final archive is only
    # ever visible once its complete byte stream is in place.
    docker exec "$PUBLISH_CONTAINER" rm -f "$PUBLISH_TEMP_PATH" >/dev/null 2>&1 || true
    docker cp "$OUTPUT" "$PUBLISH_CONTAINER:$PUBLISH_TEMP_PATH"
    docker exec "$PUBLISH_CONTAINER" sh -ceu 'test -s "$1"; mv -f "$1" "$2"' sh "$PUBLISH_TEMP_PATH" "$PUBLISH_PATH"
    PUBLISH_COMPLETED=true
fi

log "runtime migration package created: $OUTPUT"
