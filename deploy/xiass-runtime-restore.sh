#!/usr/bin/env bash
# Restore a XIASS logical migration package onto a Linux Docker host.

set -Eeuo pipefail
umask 077

PACKAGE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
INSTALL_DIR="/opt/xiass-api"
NEW_DOMAIN=""
ASSUME_YES=false
PREVIOUS_INSTALL=""
PREVIOUS_COMPOSE_FILE=""
DEPLOY_DIR=""
COMPOSE=()
ORIGINAL_ARGS=("$@")

log() { printf '[XIASS] %s\n' "$*"; }
warn() { printf '[XIASS] 警告：%s\n' "$*" >&2; }
die() { printf '[XIASS] 错误：%s\n' "$*" >&2; exit 1; }

usage() {
    cat <<'EOF'
Usage: sudo ./restore-xiass.sh [--domain new.example.com] [--install-dir /opt/xiass-api] [--yes]
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --domain)
            [ "$#" -ge 2 ] || die "--domain requires a value"
            NEW_DOMAIN="$2"
            shift 2
            ;;
        --install-dir)
            [ "$#" -ge 2 ] || die "--install-dir requires a path"
            INSTALL_DIR="$2"
            shift 2
            ;;
        --yes|-y)
            ASSUME_YES=true
            shift
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

case "$INSTALL_DIR" in
    /*) ;;
    *) die "install directory must be absolute" ;;
esac

if [ -n "$NEW_DOMAIN" ]; then
    NEW_DOMAIN="${NEW_DOMAIN#http://}"
    NEW_DOMAIN="${NEW_DOMAIN#https://}"
    NEW_DOMAIN="${NEW_DOMAIN%%/*}"
    [[ "$NEW_DOMAIN" =~ ^[A-Za-z0-9.-]+(:[0-9]{1,5})?$ ]] || die "invalid replacement domain"
fi

if [ "$(id -u)" -ne 0 ]; then
    command -v sudo >/dev/null 2>&1 || die "run this script as root or install sudo"
    exec sudo -E bash "$0" "${ORIGINAL_ARGS[@]}"
fi

install_packages() {
    local manager=""
    if command -v apt-get >/dev/null 2>&1; then
        manager="apt"
    elif command -v dnf >/dev/null 2>&1; then
        manager="dnf"
    elif command -v yum >/dev/null 2>&1; then
        manager="yum"
    elif command -v apk >/dev/null 2>&1; then
        manager="apk"
    else
        die "unsupported package manager; install Docker, Docker Compose, curl, wget, tar, gzip, and jq manually"
    fi

    case "$manager" in
        apt)
            apt-get update
            DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl wget tar gzip jq docker.io docker-compose-plugin || \
                DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl wget tar gzip jq docker.io docker-compose
            ;;
        dnf)
            dnf install -y ca-certificates curl wget tar gzip jq docker docker-compose-plugin || \
                dnf install -y ca-certificates curl wget tar gzip jq docker docker-compose
            ;;
        yum)
            yum install -y ca-certificates curl wget tar gzip jq docker docker-compose-plugin || \
                yum install -y ca-certificates curl wget tar gzip jq docker docker-compose
            ;;
        apk)
            apk add --no-cache ca-certificates curl wget tar gzip jq docker docker-cli-compose
            ;;
    esac
}

ensure_runtime() {
    local required_command
    for required_command in docker curl wget tar gzip jq sha256sum; do
        if ! command -v "$required_command" >/dev/null 2>&1; then
            log "installing required system packages"
            install_packages
            break
        fi
    done
    command -v docker >/dev/null 2>&1 || die "Docker installation failed"
    for required_command in curl wget tar gzip jq sha256sum; do
        command -v "$required_command" >/dev/null 2>&1 || die "required command is unavailable after installation: $required_command"
    done
    if command -v systemctl >/dev/null 2>&1; then
        systemctl enable --now docker >/dev/null 2>&1 || true
    fi
    docker info >/dev/null 2>&1 || die "Docker daemon is not ready"
    if docker compose version >/dev/null 2>&1; then
        COMPOSE=(docker compose)
    elif command -v docker-compose >/dev/null 2>&1; then
        COMPOSE=(docker-compose)
    else
        install_packages
        if docker compose version >/dev/null 2>&1; then
            COMPOSE=(docker compose)
        elif command -v docker-compose >/dev/null 2>&1; then
            COMPOSE=(docker-compose)
        else
            die "Docker Compose installation failed"
        fi
    fi
}

manifest_value() {
    local key="$1"
    jq -r --arg key "$key" '.[$key] // empty' "$PACKAGE_DIR/manifest.json"
}

verify_package() {
    [ -f "$PACKAGE_DIR/manifest.json" ] || die "missing manifest.json"
    [ -f "$PACKAGE_DIR/checksums.sha256" ] || die "missing checksums.sha256"
    [ -x "$PACKAGE_DIR/restore-xiass.sh" ] || die "restore script permissions are invalid"
    [ -f "$PACKAGE_DIR/payload/postgres.sql.gz" ] || die "missing PostgreSQL export"
    [ -f "$PACKAGE_DIR/payload/redis.rdb" ] || die "missing Redis export"
    [ -d "$PACKAGE_DIR/payload/app-data" ] || die "missing application data"
    [ -f "$PACKAGE_DIR/payload/.env" ] || die "missing deployment .env"
    [ -f "$PACKAGE_DIR/deploy/docker-compose.local.yml" ] || die "missing canonical docker-compose.local.yml"
    (cd "$PACKAGE_DIR" && sha256sum -c checksums.sha256)
}

compose() {
    "${COMPOSE[@]}" -f "$DEPLOY_DIR/docker-compose.local.yml" --project-directory "$DEPLOY_DIR" "$@"
}

compose_at() {
    local deploy_dir="$1" compose_file="$2"
    shift 2
    "${COMPOSE[@]}" -f "$deploy_dir/$compose_file" --project-directory "$deploy_dir" "$@"
}

stop_existing_install() {
    local existing_install="$1" existing_deploy compose_file
    existing_deploy="$existing_install/deploy"
    [ -d "$existing_deploy" ] || return 0
    for compose_file in docker-compose.local.yml docker-compose.yml; do
        if [ -f "$existing_deploy/$compose_file" ]; then
            PREVIOUS_COMPOSE_FILE="$compose_file"
            log "stopping existing XIASS containers before restore"
            compose_at "$existing_deploy" "$compose_file" down --remove-orphans
            return 0
        fi
    done
    warn "existing install has no recognized Compose file; refusing an unsafe overwrite"
    return 1
}

read_env_value() {
    local key="$1"
    awk -F= -v key="$key" '$1 == key {sub(/^[^=]*=/, ""); print; exit}' "$DEPLOY_DIR/.env" 2>/dev/null
}

replace_domain_in_payload() {
    local old_domain="$1" new_domain="$2" file escaped_old escaped_new
    [ -n "$old_domain" ] && [ -n "$new_domain" ] || return 0
    [ "$old_domain" != "$new_domain" ] || return 0
    escaped_old=$(printf '%s' "$old_domain" | sed 's/[.[\\*^$\\/]/\\\\&/g')
    escaped_new=$(printf '%s' "$new_domain" | sed 's/[&\\/]/\\\\&/g')
    while IFS= read -r -d '' file; do
        grep -Iq . "$file" || continue
        if grep -q -- "$old_domain" "$file"; then
            sed -i "s/${escaped_old}/${escaped_new}/g" "$file"
        fi
    done < <(find "$PACKAGE_DIR/payload" "$PACKAGE_DIR/deploy" -type f -print0)
}

wait_for_postgres() {
    local user db attempt
    user=$(read_env_value POSTGRES_USER)
    user="${user:-sub2api}"
    db=$(read_env_value POSTGRES_DB)
    db="${db:-sub2api}"
    for attempt in $(seq 1 90); do
        if compose exec -T postgres pg_isready -U "$user" -d "$db" >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    return 1
}

wait_for_health() {
    local port attempt
    port=$(read_env_value SERVER_PORT)
    port="${port:-8080}"
    for attempt in $(seq 1 120); do
        if curl -fsS --max-time 3 "http://127.0.0.1:${port}/health" >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    return 1
}

rollback() {
    local status=$?
    if [ "$status" -ne 0 ] && [ -n "$PREVIOUS_INSTALL" ] && [ -d "$PREVIOUS_INSTALL" ]; then
        warn "restore failed; restoring the previous installation directory"
        if [ -n "$DEPLOY_DIR" ] && [ -f "$DEPLOY_DIR/docker-compose.local.yml" ]; then
            compose down >/dev/null 2>&1 || true
        fi
        rm -rf "$INSTALL_DIR"
        mv "$PREVIOUS_INSTALL" "$INSTALL_DIR"
        if [ -n "$PREVIOUS_COMPOSE_FILE" ] && [ -f "$INSTALL_DIR/deploy/$PREVIOUS_COMPOSE_FILE" ]; then
            compose_at "$INSTALL_DIR/deploy" "$PREVIOUS_COMPOSE_FILE" up -d >/dev/null 2>&1 || \
                warn "previous XIASS containers could not be restarted automatically"
        fi
    fi
    exit "$status"
}

verify_package
ensure_runtime

SOURCE_DOMAIN=$(manifest_value source_origin)
SOURCE_DOMAIN="${SOURCE_DOMAIN#http://}"
SOURCE_DOMAIN="${SOURCE_DOMAIN#https://}"
SOURCE_DOMAIN="${SOURCE_DOMAIN%%/*}"

if ! "$ASSUME_YES"; then
    [ -r /dev/tty ] || die "non-interactive execution requires --yes"
    read -r -p "This will restore XIASS data into ${INSTALL_DIR}. Continue? [y/N]: " answer < /dev/tty || true
    [[ "$answer" =~ ^[yY]$ ]] || exit 0
fi

if [ -n "$NEW_DOMAIN" ]; then
    if [ -n "$SOURCE_DOMAIN" ]; then
        log "replacing source domain $SOURCE_DOMAIN with $NEW_DOMAIN"
        replace_domain_in_payload "$SOURCE_DOMAIN" "$NEW_DOMAIN"
    else
        warn "the source domain was not recorded in this package; no blind replacement was performed"
    fi
fi

timestamp=$(date +%Y%m%d-%H%M%S)
if [ -e "$INSTALL_DIR" ]; then
    PREVIOUS_INSTALL="${INSTALL_DIR}.before-xiass-restore-${timestamp}"
    log "preserving any existing install at $PREVIOUS_INSTALL"
    stop_existing_install "$INSTALL_DIR" || die "could not stop the existing XIASS deployment"
    mv "$INSTALL_DIR" "$PREVIOUS_INSTALL"
fi
trap rollback EXIT INT TERM

DEPLOY_DIR="$INSTALL_DIR/deploy"
mkdir -p "$DEPLOY_DIR"
cp -a "$PACKAGE_DIR/deploy/." "$DEPLOY_DIR/"
cp "$PACKAGE_DIR/payload/.env" "$DEPLOY_DIR/.env"
chmod 600 "$DEPLOY_DIR/.env"

rm -rf "$DEPLOY_DIR/data" "$DEPLOY_DIR/redis_data" "$DEPLOY_DIR/team_child_browser_data" "$DEPLOY_DIR/team_child_automation_data"
mkdir -p "$DEPLOY_DIR/data" "$DEPLOY_DIR/redis_data"
cp -a "$PACKAGE_DIR/payload/app-data/." "$DEPLOY_DIR/data/"
if [ -d "$PACKAGE_DIR/payload/team-child-browser-data" ]; then
    mkdir -p "$DEPLOY_DIR/team_child_browser_data"
    cp -a "$PACKAGE_DIR/payload/team-child-browser-data/." "$DEPLOY_DIR/team_child_browser_data/"
fi
if [ -d "$PACKAGE_DIR/payload/team-child-automation-data" ]; then
    mkdir -p "$DEPLOY_DIR/team_child_automation_data"
    cp -a "$PACKAGE_DIR/payload/team-child-automation-data/." "$DEPLOY_DIR/team_child_automation_data/"
fi

log "pulling XIASS images and starting PostgreSQL/Redis"
compose pull postgres redis xiass-api watchtower
if [ "$(read_env_value TEAM_CHILD_BROWSER_ENABLED)" = "true" ]; then
    compose --profile team-browser pull team-child-browser team-child-automation
fi
compose up -d postgres redis
wait_for_postgres || die "PostgreSQL did not become ready"

postgres_user=$(read_env_value POSTGRES_USER)
postgres_user="${postgres_user:-sub2api}"
postgres_db=$(read_env_value POSTGRES_DB)
postgres_db="${postgres_db:-sub2api}"
log "restoring PostgreSQL logical backup"
gzip -dc "$PACKAGE_DIR/payload/postgres.sql.gz" | compose exec -T postgres sh -c '
    export PGPASSWORD="${POSTGRES_PASSWORD:-}"
    exec psql -v ON_ERROR_STOP=1 -h 127.0.0.1 -U "${POSTGRES_USER:-sub2api}" -d "${POSTGRES_DB:-sub2api}"
'

log "restoring Redis snapshot"
compose stop redis
rm -rf "$DEPLOY_DIR/redis_data/appendonlydir" "$DEPLOY_DIR/redis_data/dump.rdb"
cp "$PACKAGE_DIR/payload/redis.rdb" "$DEPLOY_DIR/redis_data/dump.rdb"
compose up -d redis

log "starting XIASS API"
compose up -d xiass-api watchtower
if [ "$(read_env_value TEAM_CHILD_BROWSER_ENABLED)" = "true" ]; then
    compose --profile team-browser up -d team-child-browser team-child-automation
fi
wait_for_health || die "XIASS did not pass the health check"

trap - EXIT INT TERM
log "restore complete"
if [ -n "$PREVIOUS_INSTALL" ]; then
    log "previous installation preserved at: $PREVIOUS_INSTALL"
fi
