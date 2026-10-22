.PHONY: test vet cli app build icons

# 코어·CLI 단위 테스트 (Mac에서도 실행)
test:
	go test ./core/ ./cmd/...

# Mac·Windows 양쪽 빌드 태그로 정적 검사
vet:
	go vet -tags desktop,production ./...
	GOOS=windows CGO_ENABLED=0 go vet -tags desktop,production ./...

# 개발용: 현재 OS용 CLI
cli:
	go build -o dist/activitylogkeeper-cli ./cmd/activitylogkeeper

# 개발용: 현재 OS용 트레이·관리 창 (Mac에서 화면 확인용)
# Mac은 Wails CLI가 붙여 주던 UniformTypeIdentifiers 프레임워크를 직접 링크해야 한다.
app:
	CGO_LDFLAGS="-framework UniformTypeIdentifiers" go build -tags desktop,production -o dist/ActivityLogKeeper ./app

# Windows 배포본: dist/ActivityLogKeeper-windows-amd64.zip
build:
	./scripts/build.sh

# 아이콘 다시 그리기 (extension/icons, app/assets)
icons:
	go run ./scripts/genicon
