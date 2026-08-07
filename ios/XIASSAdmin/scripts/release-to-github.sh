#!/bin/bash
set -euo pipefail

# Codex/Xcode command-line environments can have Command Line Tools selected
# even when the full Xcode app is installed. IPA export requires full Xcode.
if [ -z "${DEVELOPER_DIR:-}" ] && [ -d "/Applications/Xcode.app/Contents/Developer" ]; then
  export DEVELOPER_DIR="/Applications/Xcode.app/Contents/Developer"
fi

if [ "$#" -ne 3 ]; then
  echo "Usage: $0 <version> <team-id> <bundle-id>"
  echo "Example: $0 1.0.1 ABCDE12345 com.example.xiassadmin"
  exit 64
fi

VERSION="$1"
TEAM_ID="$2"
BUNDLE_ID="$3"
TAG="ios-v${VERSION}"
ASSET_BASENAME="XIASSAdmin-${VERSION}"
ROOT_DIR="$(cd "$(dirname "$0")/../../.." && pwd)"
PROJECT_DIR="$ROOT_DIR/ios/XIASSAdmin"
PROJECT="$PROJECT_DIR/XIASSAdmin.xcodeproj"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/xiass-admin-release.XXXXXX")"
ARCHIVE_PATH="$WORK_DIR/XIASSAdmin.xcarchive"
EXPORT_DIR="$WORK_DIR/export"
EXPORT_OPTIONS="$WORK_DIR/ExportOptions.plist"
MANIFEST_PATH="$EXPORT_DIR/${ASSET_BASENAME}.plist"
IPA_PATH="$EXPORT_DIR/${ASSET_BASENAME}.ipa"
IPA_URL="https://github.com/xyf0104/xiass-api/releases/download/${TAG}/${ASSET_BASENAME}.ipa"

cleanup() {
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

command -v gh >/dev/null || {
  echo "GitHub CLI is required. Install it with: brew install gh"
  exit 69
}

sed "s/__TEAM_ID__/${TEAM_ID}/g" "$PROJECT_DIR/Release/ExportOptions.template.plist" > "$EXPORT_OPTIONS"
plutil -lint "$EXPORT_OPTIONS"

xcodebuild \
  -allowProvisioningUpdates \
  -project "$PROJECT" \
  -scheme XIASSAdmin \
  -configuration Release \
  -archivePath "$ARCHIVE_PATH" \
  DEVELOPMENT_TEAM="$TEAM_ID" \
  PRODUCT_BUNDLE_IDENTIFIER="$BUNDLE_ID" \
  MARKETING_VERSION="$VERSION" \
  CODE_SIGN_STYLE=Automatic \
  archive

xcodebuild -exportArchive \
  -allowProvisioningUpdates \
  -archivePath "$ARCHIVE_PATH" \
  -exportOptionsPlist "$EXPORT_OPTIONS" \
  -exportPath "$EXPORT_DIR"

EXPORTED_IPA="$(find "$EXPORT_DIR" -maxdepth 1 -type f -name '*.ipa' -print -quit)"
if [ -z "$EXPORTED_IPA" ]; then
  echo "Xcode did not export an IPA. Check the selected Team and Ad Hoc provisioning profile."
  exit 70
fi
mv "$EXPORTED_IPA" "$IPA_PATH"

unzip -p "$IPA_PATH" 'Payload/*.app/embedded.mobileprovision' > "$WORK_DIR/embedded.mobileprovision"
security cms -D -i "$WORK_DIR/embedded.mobileprovision" > "$WORK_DIR/embedded.mobileprovision.plist"
PROFILE_TASK_ALLOW="$(plutil -extract Entitlements.get-task-allow raw "$WORK_DIR/embedded.mobileprovision.plist")"
PROFILE_DEVICE="$(plutil -extract ProvisionedDevices.0 raw "$WORK_DIR/embedded.mobileprovision.plist")"
if [ "$PROFILE_TASK_ALLOW" != "false" ] || [ -z "$PROFILE_DEVICE" ]; then
  echo "Exported IPA is not signed with an installable Ad Hoc profile."
  exit 71
fi

sed \
  -e "s|__IPA_URL__|${IPA_URL}|g" \
  -e "s|__BUNDLE_ID__|${BUNDLE_ID}|g" \
  -e "s|__VERSION__|${VERSION}|g" \
  "$PROJECT_DIR/Release/OTA-manifest.template.plist" > "$MANIFEST_PATH"
plutil -lint "$MANIFEST_PATH"

if ! gh release view "$TAG" --repo xyf0104/xiass-api >/dev/null 2>&1; then
  # The service release must remain GitHub's Latest because XIASS API's web
  # updater reads that channel. iOS packages use their own independent tag.
  gh release create "$TAG" --repo xyf0104/xiass-api --title "XIASS 管理端 ${VERSION}" --notes "iOS 管理端发布包。" --latest=false
fi

gh release upload "$TAG" --repo xyf0104/xiass-api \
  "${IPA_PATH}#${ASSET_BASENAME}.ipa" \
  "${MANIFEST_PATH}#${ASSET_BASENAME}.plist" \
  --clobber

echo "Release published: https://github.com/xyf0104/xiass-api/releases/tag/${TAG}"
