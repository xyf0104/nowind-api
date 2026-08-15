#!/bin/bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TEST_DIR=$(mktemp -d)
trap 'rm -rf "$TEST_DIR"' EXIT

fail() {
    printf 'install download integrity test failed: %s\n' "$1" >&2
    exit 1
}

hash_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

mkdir -p "$TEST_DIR/fake-bin" "$TEST_DIR/payload"
printf '#!/bin/sh\nexit 0\n' > "$TEST_DIR/payload/xiass-api"
chmod +x "$TEST_DIR/payload/xiass-api"
tar -czf "$TEST_DIR/archive.tar.gz" -C "$TEST_DIR/payload" xiass-api

sed -n \
    -e '/^calculate_sha256()/,/^}/p' \
    -e '/^download_and_extract()/,/^}/p' \
    "$ROOT_DIR/deploy/install.sh" > "$TEST_DIR/install-download-lib.sh"

cat > "$TEST_DIR/fake-bin/curl" <<'EOF'
#!/bin/bash
set -eu

has_fail=false
output=""
url=""
while [ "$#" -gt 0 ]; do
    case "$1" in
        -*f*)
            has_fail=true
            shift
            ;;
        -o|--output)
            output=$2
            shift 2
            ;;
        https://*)
            url=$1
            shift
            ;;
        *)
            shift
            ;;
    esac
done

[ "$has_fail" = true ] || exit 90
[ -n "$output" ] || exit 91
[ -n "$url" ] || exit 92

case "$url" in
    */checksums.txt)
        [ "$DOWNLOAD_SCENARIO" != checksum-failure ] || exit 22
        cp "$CHECKSUM_FIXTURE" "$output"
        ;;
    *.tar.gz)
        [ "$DOWNLOAD_SCENARIO" != archive-failure ] || exit 22
        cp "$ARCHIVE_FIXTURE" "$output"
        ;;
    *)
        exit 93
        ;;
esac
EOF
chmod +x "$TEST_DIR/fake-bin/curl"

run_download() {
    local scenario=$1
    local checksum_fixture=$2
    local install_dir="$TEST_DIR/install-$scenario"

    DOWNLOAD_SCENARIO="$scenario" \
    CHECKSUM_FIXTURE="$checksum_fixture" \
    ARCHIVE_FIXTURE="$TEST_DIR/archive.tar.gz" \
    PATH="$TEST_DIR/fake-bin:$PATH" \
    INSTALL_DIR="$install_dir" \
    bash -c '
        set -e
        source "$1"
        print_info() { :; }
        print_success() { :; }
        print_error() { :; }
        msg() { printf "%s" "$1"; }
        GITHUB_REPO="xyf0104/xiass-api"
        LATEST_VERSION="v1.2.3"
        OS="linux"
        ARCH="amd64"
        SERVICE_NAME="xiass-api"
        download_and_extract
    ' bash "$TEST_DIR/install-download-lib.sh"
}

archive_name='xiass-api_1.2.3_linux_amd64.tar.gz'
checksum=$(hash_file "$TEST_DIR/archive.tar.gz")
printf '%s  %s\n' "$checksum" "$archive_name" > "$TEST_DIR/checksums-valid.txt"
printf '%064d  %s\n' 0 "$archive_name" > "$TEST_DIR/checksums-mismatch.txt"
printf '%s  %s\n' "$checksum" 'another-archive.tar.gz' > "$TEST_DIR/checksums-missing.txt"

for scenario in archive-failure checksum-failure checksum-missing checksum-mismatch; do
    fixture="$TEST_DIR/checksums-valid.txt"
    case "$scenario" in
        checksum-missing) fixture="$TEST_DIR/checksums-missing.txt" ;;
        checksum-mismatch) fixture="$TEST_DIR/checksums-mismatch.txt" ;;
    esac
    if run_download "$scenario" "$fixture" >/dev/null 2>&1; then
        fail "$scenario unexpectedly succeeded"
    fi
done

run_download success "$TEST_DIR/checksums-valid.txt" >/dev/null
test -x "$TEST_DIR/install-success/xiass-api" || fail 'verified archive was not installed'

printf 'install download integrity test passed\n'
