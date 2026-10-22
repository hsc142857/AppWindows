.PHONY: test vet cli app build

test:
	go test ./core/ ./cmd/...

vet:
	go vet -tags desktop,production ./...
	GOOS=windows CGO_ENABLED=0 go vet -tags desktop,production ./...

# Mac에서 바로 실행할 CLI (dist/naverbookingscraper-cli)
# CLI에 -cli를 붙인다: Windows·대소문자 무시 디스크에서 GUI(NaverBookingScraper)와 충돌하지 않게
cli:
	go build -o dist/naverbookingscraper-cli ./cmd/naverbookingscraper

# Mac에서 바로 실행할 GUI 앱 (dist/NaverBookingScraper.app)
app:
	./scripts/build-mac-app.sh

# Windows 배포본 (dist/NaverBookingScraper-windows-amd64.zip: GUI + CLI + 샘플 + README.txt)
build:
	./scripts/build-windows.sh
