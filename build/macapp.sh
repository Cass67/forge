#!/usr/bin/env bash
# Assembles bin/Forge.app with separate GUI and CLI/TUI binaries.
#
# macOS takes an app's Dock icon, name and Cmd-Tab entry from its bundle, not
# from the executable, so a bare binary shows up as a generic icon named after
# the process. Wails does not build the bundle for us.
set -euo pipefail

cd "$(dirname "$0")/.."

GUI_BIN="${1:-bin/forge-gui}"
CLI_BIN="${2:-bin/forge}"
APP="bin/Forge.app"
# Single source of truth: internal/version. Duplicating it here would drift.
VERSION="${FORGE_VERSION:-$(sed -n 's/^const Version = "\(.*\)"$/\1/p' internal/version/version.go)}"

if [ ! -x "$GUI_BIN" ]; then
  echo "macapp: $GUI_BIN not built; run 'just gui' first" >&2
  exit 1
fi
if [ ! -x "$CLI_BIN" ]; then
  echo "macapp: $CLI_BIN not built; run 'just build' first" >&2
  exit 1
fi

# The icon is generated rather than committed as a binary blob.
go run ./build/icongen build
iconutil -c icns build/Forge.iconset -o build/icon.icns

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$GUI_BIN" "$APP/Contents/MacOS/forge-gui"
cp "$CLI_BIN" "$APP/Contents/MacOS/forge"
cp build/icon.icns "$APP/Contents/Resources/icon.icns"

cat >"$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Forge</string>
  <key>CFBundleDisplayName</key><string>Forge</string>
  <key>CFBundleExecutable</key><string>forge-gui</string>
  <key>CFBundleIdentifier</key><string>dev.forge.app</string>
  <key>CFBundleIconFile</key><string>icon</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${VERSION}</string>
  <key>CFBundleVersion</key><string>${VERSION}</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>LSApplicationCategoryType</key><string>public.app-category.developer-tools</string>
</dict>
</plist>
PLIST

# Ad-hoc signing keeps macOS from refusing to launch a freshly assembled bundle.
codesign --force --deep --sign - "$APP" 2>/dev/null ||
  echo "macapp: ad-hoc signing unavailable; right-click → Open on first launch" >&2

echo "built: $APP"
