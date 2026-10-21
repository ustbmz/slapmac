#!/bin/zsh
# build-app.sh — build spank.app (menu bar GUI) with the spank binary embedded.
set -euo pipefail

VERSION="${1:-1.0.0}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP="$ROOT/dist/spank.app"

echo "==> Building spank v${VERSION} (darwin/arm64)..."
cd "${ROOT}"
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}" \
  go build -trimpath \
  -ldflags "-s -w -X main.version=${VERSION}" \
  -o "${ROOT}/dist/spank-bin" .

echo "==> Compiling spankbar (Swift)..."
mkdir -p "${ROOT}/dist"
swiftc -O -swift-version 5 \
  -o "${ROOT}/dist/spankbar-bin" \
  "${ROOT}/menubar/spankbar.swift" \
  -framework AppKit

echo "==> Assembling spank.app..."
rm -rf "${APP}"
mkdir -p "${APP}/Contents/MacOS" "${APP}/Contents/Resources"
cp "${ROOT}/dist/spankbar-bin" "${APP}/Contents/MacOS/spankbar"
cp "${ROOT}/dist/spank-bin"    "${APP}/Contents/Resources/spank"
cp "${ROOT}/menubar/AppIcon.icns" "${APP}/Contents/Resources/AppIcon.icns"
cp "${ROOT}/menubar/menubar-icon.png" "${APP}/Contents/Resources/menubar-icon.png"
chmod +x "${APP}/Contents/MacOS/spankbar" "${APP}/Contents/Resources/spank"

cat > "${APP}/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key>            <string>spankbar</string>
    <key>CFBundleDisplayName</key>     <string>spankbar</string>
    <key>CFBundleIdentifier</key>      <string>com.spank.spankbar</string>
    <key>CFBundleVersion</key>         <string>${VERSION}</string>
    <key>CFBundleShortVersionString</key><string>${VERSION}</string>
    <key>CFBundleExecutable</key>      <string>spankbar</string>
    <key>CFBundleIconFile</key>        <string>AppIcon</string>
    <key>CFBundlePackageType</key>     <string>APPL</string>
    <key>LSUIElement</key>             <true/>
    <key>NSHighResolutionCapable</key> <true/>
    <key>LSMinimumSystemVersion</key>  <string>12.0</string>
</dict>
</plist>
EOF

rm -f "${ROOT}/dist/spank-bin" "${ROOT}/dist/spankbar-bin"
echo "==> Done: ${APP}"
du -sh "${APP}"
