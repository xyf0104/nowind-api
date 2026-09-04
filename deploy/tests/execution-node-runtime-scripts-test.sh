#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
JOIN_SCRIPT="$ROOT/xiass-cluster-join.sh"
RUNTIME_SCRIPT="$ROOT/xiass-cluster-runtime.sh"

bash -n "$JOIN_SCRIPT"
bash -n "$RUNTIME_SCRIPT"

# Both host-side flows must preserve the existing deployment and persistent
# state. The assertions intentionally guard against destructive shortcuts.
for script in "$JOIN_SCRIPT" "$RUNTIME_SCRIPT"; do
    grep -Fq 'compose up -d --no-deps --no-build --force-recreate xiass-api' "$script"
    if grep -Fq 'compose down -v' "$script"; then
        echo "cluster script must never remove persistent volumes: $script" >&2
        exit 1
    fi
    grep -Fq 'cp "$ENV_FILE" "$OLD_ENV_FILE"' "$script"
    grep -Fq 'cp "$OLD_ENV_FILE" "$ENV_FILE"' "$script"
done

grep -Fq 'set_env_value GATEWAY_EXECUTION_NODE_ID "$JOIN_TARGET_NODE_ID"' "$JOIN_SCRIPT"
grep -Fq 'set_env_value GATEWAY_EXECUTION_NODE_DEFAULT_PROXY_ID "$target_proxy_id"' "$JOIN_SCRIPT"
grep -Fq 'set_env_value XIASS_CLUSTER_TUNNEL_TOKEN "$JOIN_TUNNEL_PROOF"' "$JOIN_SCRIPT"
grep -Fq 'set_env_value GATEWAY_EXECUTION_NODE_ID "$RUNTIME_NODE_ID"' "$RUNTIME_SCRIPT"
grep -Fq 'set_env_value XIASS_CLUSTER_TUNNEL_TOKEN "$RUNTIME_TUNNEL_TOKEN"' "$RUNTIME_SCRIPT"
grep -Fq 'compose up -d --no-deps --no-build --force-recreate xiass-api' "$RUNTIME_SCRIPT"

printf 'Execution-node runtime script contract test passed\n'
