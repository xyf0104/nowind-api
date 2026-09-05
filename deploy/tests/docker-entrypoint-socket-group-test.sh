#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENTRYPOINT="$ROOT_DIR/deploy/docker-entrypoint.sh"
# Use the currently published application image as the stable Alpine runtime.
# Versioned images can be intentionally pruned after a release, so pinning a
# historical tag here would turn retention cleanup into a false CI failure.
RUNTIME_IMAGE="${XIASS_ENTRYPOINT_TEST_IMAGE:-ghcr.io/xyf0104/xiass-api:latest}"

if ! command -v docker >/dev/null 2>&1; then
    echo "docker-entrypoint socket-group test requires Docker" >&2
    exit 1
fi

if ! docker info >/dev/null 2>&1; then
    echo "docker-entrypoint socket-group test requires a reachable Docker daemon" >&2
    exit 1
fi

# Bind sources are resolved by the daemon, not the Docker CLI. Desktop and
# Colima expose a user-space client socket on macOS while the daemon still sees
# its socket at /var/run/docker.sock inside the Linux VM.
docker_socket="${XIASS_DOCKER_SOCKET_SOURCE:-/var/run/docker.sock}"
if [ -z "${XIASS_DOCKER_SOCKET_SOURCE:-}" ] && [ "$(uname -s)" = "Linux" ]; then
    case "${DOCKER_HOST:-}" in
        unix://*) docker_socket="${DOCKER_HOST#unix://}" ;;
    esac
fi

# Exercise the repository entrypoint against the host's real Docker socket.
# The published image supplies the same Alpine runtime and su-exec binary
# without depending on the image currently being built by the release job.
docker run --rm \
    -v "$docker_socket:/var/run/docker.sock" \
    -v "$ENTRYPOINT:/tmp/source-entrypoint.sh:ro" \
    --entrypoint /bin/sh \
    "$RUNTIME_IMAGE" \
    -ec '
        cp /tmp/source-entrypoint.sh /tmp/test-entrypoint.sh
        chmod 0755 /tmp/test-entrypoint.sh
        exec /tmp/test-entrypoint.sh /bin/sh -ec '\''
            test "$(id -u)" = "1000"
            test -r /var/run/docker.sock
            socket_gid="$(stat -c "%g" /var/run/docker.sock)"
            id -G | tr " " "\n" | grep -qx "$socket_gid"
            curl -fsS --unix-socket /var/run/docker.sock http://localhost/v1.40/_ping | grep -qx OK
        '\''
    '

# Installations without host-management features must still run as the fixed
# non-root account and must not require a Docker socket.
docker run --rm \
    -v "$ENTRYPOINT:/tmp/source-entrypoint.sh:ro" \
    --entrypoint /bin/sh \
    "$RUNTIME_IMAGE" \
    -ec '
        cp /tmp/source-entrypoint.sh /tmp/test-entrypoint.sh
        chmod 0755 /tmp/test-entrypoint.sh
        exec /tmp/test-entrypoint.sh /bin/sh -ec '\''
            test "$(id -u)" = "1000"
            test ! -e /var/run/docker.sock
        '\''
    '

echo "docker-entrypoint socket-group compatibility test passed"
