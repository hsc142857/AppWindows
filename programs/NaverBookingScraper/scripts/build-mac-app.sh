#!/bin/bash
# Mac에서 바로 실행해 볼 dist/NaverBookingScraper.app 을 만든다 (서명 없음, 로컬 테스트용).
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"
APP="$ROOT/dist/NaverBookingScraper.app"
VERSION="$(sed -n 's/.*Version *= *"\(.*\)"/\1/p' core/paths.go)"

# Wails CLI가 붙여 주던 UniformTypeIdentifiers 프레임워크를 직접 링크한다.
CGO_LDFLAGS="-framework UniformTypeIdentifiers" go build -tags desktop,production -trimpath \
  -o "$ROOT/dist/NaverBookingScraper" ./app

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$ROOT/dist/NaverBookingScraper" "$APP/Contents/MacOS/NaverBookingScraper"

# 아이콘: app/assets/icon.png → AppIcon.icns (sips·iconutil은 macOS 기본 도구)
ICONSET="$(mktemp -d)/AppIcon.iconset"
mkdir -p "$ICONSET"
for s in 16 32 128 256 512; do
  sips -z $s $s app/assets/icon.png --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
  d=$((s * 2))
  sips -z $d $d app/assets/icon.png --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/AppIcon.icns"

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>NaverBookingScraper</string>
  <key>CFBundleDisplayName</key><string>네이버예약 스크랩</string>
  <key>CFBundleExecutable</key><string>NaverBookingScraper</string>
  <key>CFBundleIdentifier</key><string>com.appwindows.naverbookingscraper</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${VERSION}</string>
  <key>CFBundleVersion</key><string>${VERSION}</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/></dict>
</dict>
</plist>
PLIST

codesign --force --deep --sign - "$APP" >/dev/null 2>&1 || true
echo "앱: $APP"
