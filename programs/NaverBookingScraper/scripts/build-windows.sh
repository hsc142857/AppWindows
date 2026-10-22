#!/bin/bash
# Windows 배포본을 만든다 (Mac에서 크로스 빌드).
#   dist/NaverBookingScraper-windows-<arch>/
#     NaverBookingScraper.exe        GUI (관리 창)
#     naverbookingscraper-cli.exe    CLI (GUI와 이름이 대소문자만 다르면 Windows에서 충돌하므로 -cli)
#     sample-reservations.json       가져오기 테스트용 샘플
#     README.txt                     사용법 (docs/사용법.txt)
#   dist/NaverBookingScraper-windows-<arch>.zip
# ARCH=arm64 ./scripts/build-windows.sh 로 ARM용도 만들 수 있다 (기본 amd64).
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"
ARCH="${ARCH:-amd64}"
VERSION="$(sed -n 's/.*Version *= *"\(.*\)"/\1/p' core/paths.go)"
WINRES="github.com/tc-hib/go-winres@v0.3.3"
OUT="$ROOT/dist/NaverBookingScraper-windows-$ARCH"

# exe 아이콘·버전 정보·매니페스트(DPI 인식) — .syso는 빌드 때만 쓰고 지운다
go run "$WINRES" simply --arch "$ARCH" --icon app/assets/icon.png --manifest gui \
  --product-name "NaverBookingScraper" --file-description "네이버예약 스크랩" \
  --product-version "$VERSION" --file-version "$VERSION" --out app/rsrc
go run "$WINRES" simply --arch "$ARCH" --icon app/assets/icon.png --manifest cli \
  --product-name "NaverBookingScraper" --file-description "네이버예약 스크랩 CLI" \
  --product-version "$VERSION" --file-version "$VERSION" --out cmd/naverbookingscraper/rsrc
trap 'rm -f app/rsrc_windows_*.syso cmd/naverbookingscraper/rsrc_windows_*.syso' EXIT

rm -rf "$OUT" "$OUT.zip"
mkdir -p "$OUT"
export GOOS=windows GOARCH="$ARCH" CGO_ENABLED=0
go build -trimpath -tags desktop,production -ldflags "-s -w -H windowsgui" -o "$OUT/NaverBookingScraper.exe" ./app
go build -trimpath -ldflags "-s -w" -o "$OUT/naverbookingscraper-cli.exe" ./cmd/naverbookingscraper
cp testdata/sample-reservations.json "$OUT/sample-reservations.json"
# 메모장이 확실히 UTF-8로 열도록 BOM을 붙이고 줄바꿈을 CRLF로
printf '\xef\xbb\xbf' > "$OUT/README.txt"
sed 's/$/\r/' docs/사용법.txt >> "$OUT/README.txt"

(cd "$ROOT/dist" && zip -qr "NaverBookingScraper-windows-$ARCH.zip" "NaverBookingScraper-windows-$ARCH")
echo "배포 폴더: $OUT"
echo "압축 파일: $OUT.zip"
