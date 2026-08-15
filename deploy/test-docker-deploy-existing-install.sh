#!/bin/bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TEST_DIR=$(mktemp -d)
trap 'rm -rf "$TEST_DIR"' EXIT

fail() {
    printf 'docker deploy existing-install test failed: %s\n' "$1" >&2
    exit 1
}

file_mode() {
    if stat -c '%a' "$1" >/dev/null 2>&1; then
        stat -c '%a' "$1"
    else
        stat -f '%Lp' "$1"
    fi
}

mkdir -p "$TEST_DIR/fake-bin"
for command_name in curl wget openssl; do
    cat > "$TEST_DIR/fake-bin/$command_name" <<'EOF'
#!/bin/sh
: > "$EXTERNAL_COMMAND_MARKER"
exit 99
EOF
    chmod +x "$TEST_DIR/fake-bin/$command_name"
done

run_existing_file_case() {
    local case_name=$1
    local existing_file=$2
    local content=$3
    local mode=$4
    local case_dir="$TEST_DIR/$case_name"
    local target="$case_dir/$existing_file"
    local expected="$case_dir/expected"
    local marker="$case_dir/external-command-called"
    local before_mode
    local after_mode

    mkdir -p "$case_dir"
    printf '%s' "$content" > "$target"
    printf '%s' "$content" > "$expected"
    chmod "$mode" "$target"
    before_mode=$(file_mode "$target")

    if (cd "$case_dir" && \
        EXTERNAL_COMMAND_MARKER="$marker" \
        PATH="$TEST_DIR/fake-bin:/usr/bin:/bin" \
        bash "$ROOT_DIR/deploy/docker-deploy.sh" >output.log 2>&1); then
        fail "$case_name unexpectedly succeeded"
    fi

    cmp -s "$target" "$expected" || fail "$existing_file content changed in $case_name"
    after_mode=$(file_mode "$target")
    [ "$after_mode" = "$before_mode" ] || fail "$existing_file mode changed in $case_name"
    [ ! -e "$marker" ] || fail "$case_name invoked download or secret generation"
    grep -Fq 'Existing deployment files detected' "$case_dir/output.log" || \
        fail "$case_name did not explain the refusal"
}

run_existing_file_case env-only .env 'JWT_SECRET=keep-this-secret\n' 600
run_existing_file_case compose-only docker-compose.yml 'services: {}\n' 640

printf 'docker deploy existing-install test passed\n'
