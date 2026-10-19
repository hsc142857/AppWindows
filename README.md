# AppWindows

Windows용 가벼운 유틸리티 프로그램을 모아 관리하는 저장소입니다.
각 프로그램은 `programs/` 아래 독립 Go 모듈로 두고, 이 README의 목록으로 관리합니다.
개발은 Mac에서 하고 Windows exe로 크로스 빌드합니다 (cgo 없이).

## 프로그램 목록

| # | 이름 | 경로 | 요약 |
|---|------|------|------|
| 1 | [PageVault](programs/PageVault/) | `programs/PageVault` | 로그인된 Chrome 페이지를 주기적으로 SQLite에 백업하고 Postgres로 동기화 |

### 1. PageVault

Chrome 확장(MV3)이 지정한 URL을 주기적으로 열어 제목·본문·HTML을 읽고,
Native Messaging 호스트(`pagevault.exe`)가 로컬 SQLite에 저장한 뒤 원격 Postgres로 동기화합니다.

- **수집**: 이미 로그인된 Chrome 세션 사용, 주기 30초 이상, 로그인 만료 감지
- **저장**: 내용이 같으면 새 행 대신 확인 시각만 갱신, Postgres가 끊겨도 다음에 재전송
- **제공 형태**: 트레이 앱·관리 창(`PageVault.exe`) · CLI(`pagevault.exe`) · Chrome 확장

```bash
cd programs/PageVault
make test     # 테스트
make build    # dist/PageVault-windows-amd64.zip
```

설계: [programs/PageVault/docs/PageVault_설계.md](programs/PageVault/docs/PageVault_설계.md)

## 저장소 구조

```
AppWindows/
├── README.md                 # 프로그램 목록·요약
├── Makefile                  # make list / test / build — 각 프로그램 Makefile을 순회
├── go.work                   # 프로그램 모듈을 묶는 Go 워크스페이스
└── programs/
    └── PageVault/            # 개별 프로그램 (Go 모듈)
        ├── go.mod
        ├── Makefile          # test / vet / build
        ├── core/             # 로직 (Mac에서 go test)
        ├── cmd/<cli>/        # CLI
        ├── app/              # GUI (Wails 창 / 트레이)
        ├── scripts/          # build.sh (Windows 배포본)
        └── docs/
```

## 새 프로그램 추가

1. `programs/<이름>/`에 `go mod init <이름>` 후 위 구조로 구성합니다.
2. `go.work`에 `use ./programs/<이름>`을 추가합니다.
3. `Makefile`에 `test`·`build` 타깃을 둡니다 (루트 `make test`/`make build`가 자동으로 실행).
4. 위 **프로그램 목록**에 한 줄 요약을 넣습니다.

GUI는 Wails v2를 CLI 없이 `go build -tags desktop,production -ldflags "-H windowsgui"`로 빌드하고,
exe 아이콘·매니페스트는 `go-winres`로 넣습니다 (PageVault `scripts/build.sh` 참고).
