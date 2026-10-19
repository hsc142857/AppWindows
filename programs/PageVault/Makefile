.PHONY: test test-pg vet cli app build icons

# 코어 단위 테스트 (Mac에서도 실행)
test:
	go test ./core/ ./cmd/...

# 실제 Postgres로 동기화 테스트: make test-pg PG=postgres://user:pass@host:5432/db?sslmode=disable
test-pg:
	PAGEVAULT_TEST_PG='$(PG)' go test -count=1 -run TestSyncIntegration -v ./core/

# Mac·Windows 양쪽 빌드 태그로 정적 검사
vet:
	go vet -tags desktop,production ./...
	GOOS=windows CGO_ENABLED=0 go vet -tags desktop,production ./...

# 개발용: 현재 OS용 CLI
cli:
	go build -o dist/pagevault ./cmd/pagevault

# 개발용: 현재 OS용 트레이·관리 창 (Mac에서 화면 확인용)
app:
	go build -tags desktop,production -o dist/PageVault ./app

# Windows 배포본 (dist/PageVault-windows-amd64.zip)
build:
	./scripts/build.sh

icons:
	go run ./scripts/genicon
