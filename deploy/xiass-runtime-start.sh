#!/usr/bin/env bash
# Start XIASS in two phases: core services first, browser services second.
# This script intentionally never removes volumes or data.

set -Eeuo pipefail

INSTALL_DIR="${INSTALL_DIR:-}"
DEPLOY_DIR="${DEPLOY_DIR:-}"
COMPOSE_FILE="${COMPOSE_FILE:-}"
COMPOSE_BUILD_FILE="${COMPOSE_BUILD_FILE:-}"
PERSISTENCE_MODE="${PERSISTENCE_MODE:-}"
BUILD_MODE="${BUILD_MODE:-}"
CORE_READY_DELAY_SECONDS="${XIASS_CORE_READY_DELAY_SECONDS:-${TEAM_CHILD_BROWSER_START_DELAY_SECONDS:-5}}"
TEAM_CHILD_BROWSER_ENABLED="${TEAM_CHILD_BROWSER_ENABLED:-}"

log() { printf '[XIASS] %s\n' "$*"; }
warn() { printf '[XIASS] 警告：%s\n' "$*" >&2; }
die() { printf '[XIASS] 错误：%s\n' "$*" >&2; exit 1; }

read_env_value() {
    local key="$1"
    awk -F= -v key="$key" '$1 == key {sub(/^[^=]*=/, ""); print; exit}' "$DEPLOY_DIR/.env" 2>/dev/null
}

resolve_compose() {
    if docker compose version >/dev/null 2>&1; then
        COMPOSE=(docker compose)
    elif command -v docker-compose >/dev/null 2>&1; then
        COMPOSE=(docker-compose)
    else
        die "缺少 Docker Compose。"
    fi

    if [ -z "$DEPLOY_DIR" ]; then
        [ -n "$INSTALL_DIR" ] || die "INSTALL_DIR 未设置。"
        DEPLOY_DIR="$INSTALL_DIR/deploy"
    fi
    [ -d "$DEPLOY_DIR" ] || die "未找到部署目录：$DEPLOY_DIR"
    [ -f "$DEPLOY_DIR/.env" ] || die "未找到环境配置：$DEPLOY_DIR/.env"

    if [ -z "$COMPOSE_FILE" ]; then
        if [ "$PERSISTENCE_MODE" = "named" ]; then
            COMPOSE_FILE="$DEPLOY_DIR/docker-compose.yml"
        else
            COMPOSE_FILE="$DEPLOY_DIR/docker-compose.local.yml"
        fi
    fi
    [ -f "$COMPOSE_FILE" ] || die "未找到 Docker Compose 配置：$COMPOSE_FILE"

    if [ -z "$BUILD_MODE" ]; then
        BUILD_MODE=$(read_env_value XIASS_BUILD_MODE)
        [ -n "$BUILD_MODE" ] || BUILD_MODE=$(read_env_value NOWIND_BUILD_MODE)
        [ -n "$BUILD_MODE" ] || BUILD_MODE="image"
    fi
    if [ -z "$COMPOSE_BUILD_FILE" ] && [ "$BUILD_MODE" = "source" ] && [ -f "$DEPLOY_DIR/docker-compose.build.yml" ]; then
        COMPOSE_BUILD_FILE="$DEPLOY_DIR/docker-compose.build.yml"
    fi
    if [ -z "${XIASS_CORE_READY_DELAY_SECONDS:-}" ] && [ -z "${TEAM_CHILD_BROWSER_START_DELAY_SECONDS:-}" ]; then
        CORE_READY_DELAY_SECONDS=$(read_env_value TEAM_CHILD_BROWSER_START_DELAY_SECONDS)
        CORE_READY_DELAY_SECONDS="${CORE_READY_DELAY_SECONDS:-5}"
    fi
}

compose() {
    local args=(-f "$COMPOSE_FILE")
    if [ -n "$COMPOSE_BUILD_FILE" ] && [ -f "$COMPOSE_BUILD_FILE" ]; then
        args+=(-f "$COMPOSE_BUILD_FILE")
    fi
    "${COMPOSE[@]}" "${args[@]}" --project-directory "$DEPLOY_DIR" "$@"
}

profile_compose() {
    compose --profile team-browser "$@"
}

service_exists() {
    local service="$1" profile="${2:-false}" services
    if [ "$profile" = "true" ]; then
        services=$(profile_compose config --services 2>/dev/null || true)
    else
        services=$(compose config --services 2>/dev/null || true)
    fi
    printf '%s\n' "$services" | awk -v service="$service" '$0 == service { found = 1 } END { exit !found }'
}

wait_for_core_health() {
    local port attempt
    port=$(read_env_value SERVER_PORT)
    port="${port:-8080}"
    log "等待 XIASS 主服务健康检查通过..."
    for attempt in $(seq 1 120); do
        if curl -fsS --max-time 3 "http://127.0.0.1:${port}/health" >/dev/null 2>&1; then
            log "XIASS 主服务健康检查通过。"
            return 0
        fi
        sleep 2
    done
    return 1
}

container_state() {
    docker inspect --type container --format '{{.State.Status}}' "$1" 2>/dev/null || true
}

container_health() {
    docker inspect --type container --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$1" 2>/dev/null || true
}

wait_for_automation_health() {
    local container="$1" attempt state health
    log "等待 Team 自动化服务就绪..."
    for attempt in $(seq 1 60); do
        state=$(container_state "$container")
        health=$(container_health "$container")
        if [ "$health" = "healthy" ]; then
            log "Team 自动化服务已就绪。"
            return 0
        fi
        if [ "$state" = "exited" ] || [ "$state" = "dead" ]; then
            return 1
        fi
        sleep 3
    done
    return 1
}

start_core() {
    local services=()
    for service in postgres redis watchtower xiass-api; do
        if service_exists "$service"; then
            services+=("$service")
        fi
    done
    service_exists xiass-api || die "Compose 中缺少 xiass-api 服务。"

    if [ "${XIASS_RUNTIME_CORE_READY:-false}" != "true" ]; then
        if [ "$BUILD_MODE" = "source" ]; then
            log "准备 XIASS 主服务镜像（浏览器组件尚未开始）..."
            compose build xiass-api
        else
            log "拉取 XIASS 主服务镜像（浏览器组件尚未开始）..."
            local pull_services=(xiass-api)
            service_exists watchtower && pull_services+=(watchtower)
            compose pull "${pull_services[@]}"
        fi
    else
        log "沿用已准备好的 XIASS 主服务镜像。"
    fi

    log "启动 PostgreSQL、Redis、Watchtower 和 XIASS 主服务..."
    compose up -d --no-build "${services[@]}"
    wait_for_core_health || die "XIASS 主服务未在限定时间内通过健康检查。"
}

browser_enabled() {
    local value="$TEAM_CHILD_BROWSER_ENABLED"
    [ -n "$value" ] || value=$(read_env_value TEAM_CHILD_BROWSER_ENABLED)
    case "${value,,}" in
        1|true|yes|on) return 0 ;;
        *) return 1 ;;
    esac
}

start_browser_stack() {
    if ! browser_enabled; then
        log "TEAM_CHILD_BROWSER_ENABLED 未开启，跳过浏览器组件阶段。"
        return 0
    fi
    if ! service_exists team-child-browser true || ! service_exists team-child-automation true; then
        warn "当前 Compose 没有 Team 浏览器服务定义，主服务保持正常；跳过浏览器组件。"
        return 0
    fi

    log "主服务已稳定运行，等待 ${CORE_READY_DELAY_SECONDS} 秒后开始浏览器组件阶段..."
    sleep "$CORE_READY_DELAY_SECONDS"

    if [ "$BUILD_MODE" = "source" ]; then
        log "构建 Team 自动化组件..."
        if ! profile_compose build team-child-automation; then
            warn "Team 自动化组件构建失败；主服务保持运行，可稍后重试。"
            return 0
        fi
    else
        log "拉取 Chromium 和 Team 自动化组件..."
        if ! profile_compose pull team-child-browser team-child-automation; then
            warn "Team 组件镜像拉取未完全成功，尝试使用本地缓存或构建自动化组件。"
            profile_compose build team-child-automation >/dev/null 2>&1 || true
        fi
    fi

    log "启动 Chromium 和 Team 自动化服务..."
    if ! profile_compose up -d --no-build team-child-browser team-child-automation; then
        warn "浏览器组件启动失败；XIASS 主服务保持运行，可稍后重试。"
        return 0
    fi

    local automation_container
    automation_container=$(profile_compose ps -q team-child-automation 2>/dev/null | head -n 1 || true)
    if [ -z "$automation_container" ] || ! wait_for_automation_health "$automation_container"; then
        warn "Team 自动化服务未及时就绪；XIASS 主服务保持运行，请检查组件日志。"
    fi
}

main() {
    command -v curl >/dev/null 2>&1 || die "缺少 curl。"
    command -v docker >/dev/null 2>&1 || die "缺少 Docker。"
    [[ "$CORE_READY_DELAY_SECONDS" =~ ^[0-9]+$ ]] || die "启动延迟必须是非负整数秒。"
    [ "$CORE_READY_DELAY_SECONDS" -le 120 ] || die "启动延迟不能超过 120 秒。"
    resolve_compose
    start_core
    start_browser_stack
}

main "$@"
