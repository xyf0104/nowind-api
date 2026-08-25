#!/bin/sh
set -e

# Fix data directory permissions when running as root.
# Docker named volumes / host bind-mounts may be owned by root,
# preventing the non-root runtime user (UID/GID 1000) from writing files.
if [ "$(id -u)" = "0" ]; then
    mkdir -p /app/data
    # Use || true to avoid failure on read-only mounted files (e.g. config.yaml:ro)
    chown -R 1000:1000 /app/data 2>/dev/null || true

    # The admin updater and host-management features use the mounted Docker
    # socket. Its group ID is assigned by the host and commonly differs from
    # the image's fixed runtime GID, so grant the non-root user that exact
    # supplementary group before dropping privileges. Never loosen the socket
    # permissions: it remains readable only by root and its owning group.
    if [ -S /var/run/docker.sock ]; then
        docker_socket_gid="$(stat -c '%g' /var/run/docker.sock 2>/dev/null || true)"
        case "$docker_socket_gid" in
            ''|*[!0-9]*)
                ;;
            *)
                docker_socket_group="$(awk -F: -v gid="$docker_socket_gid" '$3 == gid { print $1; exit }' /etc/group)"
                if [ -z "$docker_socket_group" ]; then
                    docker_socket_group="xiass-docker"
                    addgroup -g "$docker_socket_gid" "$docker_socket_group" >/dev/null 2>&1
                fi
                addgroup xiass "$docker_socket_group" >/dev/null 2>&1 || true
                ;;
        esac
    fi

    # Re-invoke this script as the fixed runtime UID/GID so flag detection
    # also runs under the correct user. Using the account name makes su-exec
    # initialize the Docker socket supplementary group configured above.
    exec su-exec xiass "$0" "$@"
fi

# Compatibility: if the first arg looks like a flag (e.g. --help),
# prepend the default binary so it behaves the same as the old
# ENTRYPOINT ["/app/xiass-api"] style.
if [ "${1#-}" != "$1" ]; then
    set -- /app/xiass-api "$@"
fi

exec "$@"
