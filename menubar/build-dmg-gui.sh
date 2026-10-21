#!/bin/zsh
# build-dmg-gui.sh — package spank.app into a distributable DMG.
#
# The DMG uses the classic drag-to-Applications layout:
#   spank.app          — the menu bar app (spank binary embedded)
#   Applications       — symlink to /Applications
#   安装说明.txt        — install & Gatekeeper instructions
set -euo pipefail

VERSION="${1:-1.0.0}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STAGING="${ROOT}/dist/dmg-gui-staging"
OUTPUT="${ROOT}/dist/spankbar_${VERSION}_mac_arm64.dmg"

echo "==> Building spank.app v${VERSION}..."
"${ROOT}/menubar/build-app.sh" "${VERSION}"

echo "==> Staging DMG contents..."
rm -rf "${STAGING}"
mkdir -p "${STAGING}"
cp -R "${ROOT}/dist/spank.app" "${STAGING}/"
ln -s /Applications "${STAGING}/Applications"

cat > "${STAGING}/安装说明.txt" <<'EOF'
spank — 拍打你的 MacBook，它会惨叫回来 🖐️💻
================================================

【安装步骤】

1. 把 spank.app 拖进右边的 Applications 文件夹
2. 首次打开前，先在「终端」执行一次（绕过 Gatekeeper，因为本应用未经 Apple 公证）:

   xattr -cr /Applications/spank.app

3. 打开 spank.app，菜单栏会出现 👋 图标
4. 点 👋 → 「启动 spank」，输入一次管理员密码
   （用于安装传感器驱动所需的 root 权限组件，之后不再需要密码）
5. 用力拍你的 MacBook 掌托或顶盖 🎉

【系统要求】

- Apple Silicon（M2 及以上）MacBook
- macOS 12+

【注意】

- spank 需要读取加速度传感器，属正常现象
- 音量跟随系统音量，在菜单栏调好音量再玩
- 详细用法见 app 内「打开 spank 日志」或项目 README
EOF

echo "==> Creating DMG..."
rm -f "${OUTPUT}"
hdiutil create \
  -volname "spankbar v${VERSION}" \
  -srcfolder "${STAGING}" \
  -format UDZO \
  -ov \
  "${OUTPUT}"

rm -rf "${STAGING}"
echo "==> Done: ${OUTPUT}"
ls -lh "${OUTPUT}"
