#!/bin/zsh
# build-dmg.sh — build a distributable DMG for spank.
#
# Usage:
#   ./scripts/build-dmg.sh [version]
#
# The DMG contains:
#   spank        — the compiled arm64 binary
#   install.sh   — one-shot installer (copies to /usr/local/bin)
#   README.md    — usage docs
#   LICENSE      — MIT license
set -euo pipefail

VERSION="${1:-1.0.0}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STAGING="${ROOT}/dist/dmg-staging"
OUTPUT="${ROOT}/dist/spank_${VERSION}_darwin_arm64.dmg"

echo "==> Building spank v${VERSION} (darwin/arm64)..."
cd "${ROOT}"
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}" \
  go build -trimpath \
  -ldflags "-s -w -X main.version=${VERSION}" \
  -o "${STAGING}/spank" .

echo "==> Staging files..."
cp "${ROOT}/README.md" "${ROOT}/LICENSE" "${STAGING}/"

cat > "${STAGING}/install.sh" <<'EOF'
#!/bin/zsh
# spank installer — copies the binary to /usr/local/bin
set -e
DIR="$(cd "$(dirname "$0")" && pwd)"
if [ "$(whoami)" = "root" ]; then
  DEST=/usr/local/bin/spank
  cp "${DIR}/spank" "${DEST}"
  chmod 755 "${DEST}"
else
  DEST="${HOME}/.local/bin/spank"
  mkdir -p "${HOME}/.local/bin"
  cp "${DIR}/spank" "${DEST}"
  chmod 755 "${DEST}"
  echo "note: installed to ${DEST} (no root)."
  echo "note: spank still needs 'sudo' at runtime for accelerometer access;"
  echo "note: it will use the non-root copy at ${DEST}."
fi
echo "installed: ${DEST}"
echo "run:       sudo spank"
EOF
chmod +x "${STAGING}/install.sh" "${STAGING}/spank"

echo "==> Creating DMG..."
rm -f "${OUTPUT}"
hdiutil create \
  -volname "spank v${VERSION}" \
  -srcfolder "${STAGING}" \
  -format UDZO \
  -ov \
  "${OUTPUT}"

echo "==> Done: ${OUTPUT}"
ls -lh "${OUTPUT}"
