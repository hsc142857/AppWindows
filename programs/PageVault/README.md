# PageVault

로그인된 Chrome에서 지정한 웹페이지를 주기적으로 읽어 **로컬 SQLite에 백업**하고,
**원격 PostgreSQL로 동기화**하는 Windows 프로그램입니다.

AppWindows 프로그램 목록의 1번 항목입니다. 전체 목록은 [저장소 README](../../README.md)를 보세요.

## 구성

```
Chrome 확장 (MV3, extension/)
  └ 알람마다 Native Messaging으로 pagevault.exe 실행 → config → 최소화 창에서 URL 순회
    → save(제목·본문·HTML) → sync
        ↓ stdin/stdout (4바이트 길이 + JSON)
pagevault.exe  (cmd/pagevault)  CLI 겸 Native Messaging 호스트
  └ SQLite 저장 → Postgres 동기화 (core/)
        ↑ 같은 SQLite (WAL)
PageVault.exe  (app/)  트레이 상주 + 관리 창(Wails, --window)
```

| 폴더 | 내용 |
|---|---|
| `core/` | 저장소(SQLite), 로그인 감지, Native Messaging, Postgres 동기화, 호스트 등록 |
| `cmd/pagevault/` | CLI · Native Messaging 호스트 |
| `app/` | 트레이(fyne systray) + 관리 창(Wails v2, `frontend/dist`는 빌드 없는 순수 HTML/JS) |
| `extension/` | Chrome 확장 (manifest `key`로 ID `aojpddnbebpeglnlidlnhdkgpchgfdpe` 고정) |
| `scripts/` | `build.sh`(Windows 배포본), `genicon`(아이콘 생성) |
| `docs/` | [설계](docs/PageVault_설계.md), [사용법](docs/사용법.txt)(배포본의 README.txt) |

## 빌드 (Mac에서 Windows용 크로스 빌드)

```bash
make test     # 코어 단위 테스트
make vet      # Mac·Windows 빌드 태그로 정적 검사
make build    # dist/PageVault-windows-amd64.zip
```

- wails CLI·Node 없이 `go build -tags desktop,production`으로 빌드합니다.
- 실제 Postgres 동기화 테스트: `make test-pg PG='postgres://user:pass@127.0.0.1:5432/db?sslmode=disable'`
- 개발 중 Mac에서 화면 확인: `make app && PAGEVAULT_HOME=/tmp/pv dist/PageVault --window`

## CLI

```
pagevault status
pagevault target list | add <url> [이름] | remove <id> | enable <id> | disable <id>
pagevault interval [초]          # 최소 30
pagevault delay [초]             # 0~120
pagevault snapshots [개수] [--url <url>]
pagevault show <id> [--html]
pagevault pg set <postgres://...> [--table 이름] | show | test | clear
pagevault sync
pagevault install-host [확장ID...] | uninstall-host
```

데이터: `%LOCALAPPDATA%\PageVault\` (`pagevault.sqlite`, `postgres.json`, `host.log`, `bin\pagevault.exe`).
`PAGEVAULT_HOME` 환경변수로 바꿀 수 있습니다.

## 알려진 한계

- 수집은 Chrome이 켜져 있는 동안에만 됩니다.
- 확장은 처음 한 번 "압축해제된 확장 프로그램 로드"로 설치합니다.
- 주기를 바꾸면 다음 라운드부터 적용됩니다.
