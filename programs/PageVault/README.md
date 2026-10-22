# PageVault

로그인된 Chrome에서 지정한 웹페이지를 주기적으로 읽어 **로컬 SQLite에 백업**하고,
**Web API로 업로드**하는 Windows 프로그램입니다.

AppWindows 프로그램 목록의 1번 항목입니다. 전체 목록은 [저장소 README](../../README.md)를 보세요.

## 구성

```
Chrome 확장 (MV3, extension/)
  └ 알람마다 Native Messaging으로 bin\pagevault.exe(설치된 호스트) 실행 → config → 최소화 창에서 URL 순회
    → save(제목·본문·HTML) → sync
        ↓ stdin/stdout (4바이트 길이 + JSON)
pagevault-cli.exe  (cmd/pagevault)  CLI 겸 Native Messaging 호스트 — install-host 하면 bin\pagevault.exe로 복사
  └ SQLite 저장 → Web API 업로드 (core/, shared/apiclient)
        ↑ 같은 SQLite (WAL)
PageVault.exe  (app/)  트레이 상주 + 관리 창(Wails, --window)
```

| 폴더 | 내용 |
|---|---|
| `core/` | 저장소(SQLite), 로그인 감지, Native Messaging, Web API 업로드, 호스트 등록 |
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
- 업로드 테스트는 가짜 API 서버(httptest)로 돌아가므로 실제 서버가 필요 없습니다.
- 개발 중 Mac에서 화면 확인: `make app && PAGEVAULT_HOME=/tmp/pv dist/PageVault --window`
- 배포 폴더의 CLI는 `pagevault-cli.exe`입니다 (Windows는 대소문자를 구분하지 않아 `PageVault.exe`와 `pagevault.exe`가 충돌).

## CLI

```
pagevault-cli status
pagevault-cli target list | add <url> [이름] | remove <id> | enable <id> | disable <id>
pagevault-cli interval [초]          # 최소 30
pagevault-cli delay [초]             # 0~120
pagevault-cli snapshots [개수] [--url <url>]
pagevault-cli show <id> [--html]
pagevault-cli api set <https://api.example.com> [토큰] | show | test | clear   # 토큰 생략 시 입력받음
pagevault-cli sync [--retry-rejected]
pagevault-cli install-host [확장ID...] | uninstall-host
```

데이터: `%LOCALAPPDATA%\PageVault\` (`pagevault.sqlite`, `api.json`, `host.log`, `bin\pagevault.exe`).
`PAGEVAULT_HOME` 환경변수로 바꿀 수 있습니다.

## 알려진 한계

- 수집은 Chrome이 켜져 있는 동안에만 됩니다.
- 확장은 처음 한 번 "압축해제된 확장 프로그램 로드"로 설치합니다.
- 주기를 바꾸면 다음 라운드부터 적용됩니다.
