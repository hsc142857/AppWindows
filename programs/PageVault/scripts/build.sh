#!/bin/bash
# Windows 배포본을 만든다 (Mac·Linux·Windows 어디서든 크로스 빌드).
#   dist/PageVault-windows-<arch>/  PageVault.exe, pagevault-cli.exe, extension/, README.txt
# (CLI에 -cli를 붙인다: Windows는 대소문자를 구분하지 않아 PageVault.exe와 pagevault.exe가 충돌한다)
#   dist/PageVault-windows-<arch>.zip
# ARCH=arm64 ./scripts/build.sh 로 ARM용도 만들 수 있다 (기본 amd64).
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"
ARCH="${ARCH:-amd64}"
VERSION="$(sed -n 's/.*Version *= *"\(.*\)"/\1/p' core/paths.go)"
WINRES="github.com/tc-hib/go-winres@v0.3.3"
OUT="$ROOT/dist/PageVault-windows-$ARCH"

# exe 아이콘·매니페스트(DPI 인식) 리소스 (.syso는 빌드 때만 쓰고 커밋하지 않는다)
go run "$WINRES" simply --arch "$ARCH" --icon app/assets/icon.png --manifest gui \
  --product-name PageVault --file-description "PageVault" \
  --product-version "$VERSION" --file-version "$VERSION" --out app/rsrc
go run "$WINRES" simply --arch "$ARCH" --icon app/assets/icon.png --manifest cli \
  --product-name PageVault --file-description "PageVault CLI / Chrome native host" \
  --product-version "$VERSION" --file-version "$VERSION" --out cmd/pagevault/rsrc
trap 'rm -f app/rsrc_windows_*.syso cmd/pagevault/rsrc_windows_*.syso' EXIT

rm -rf "$OUT" "$OUT.zip"
mkdir -p "$OUT"
export GOOS=windows GOARCH="$ARCH" CGO_ENABLED=0
go build -trimpath -tags desktop,production -ldflags "-s -w -H windowsgui" -o "$OUT/PageVault.exe" ./app
go build -trimpath -ldflags "-s -w" -o "$OUT/pagevault-cli.exe" ./cmd/pagevault
cp -R extension "$OUT/extension"
cp docs/사용법.txt "$OUT/README.txt"  # zip 안 한글 파일 이름이 깨지지 않도록 영문 이름

(cd "$ROOT/dist" && zip -qr "PageVault-windows-$ARCH.zip" "PageVault-windows-$ARCH")
echo "배포 폴더: $OUT"
echo "압축 파일: $OUT.zip"
