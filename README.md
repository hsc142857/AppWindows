# AppWindows

Windows용 가벼운 유틸리티 프로그램을 모아 관리하는 저장소입니다.
각 프로그램은 `programs/` 아래 독립 Go 모듈로 두고, 이 README의 목록으로 관리합니다.
개발은 Mac에서 하고 Windows exe로 크로스 빌드합니다 (cgo 없이).

> **데이터 원칙**: 프로그램은 DB(Postgres 등)에 직접 연결하지 않고 **Web API**로 데이터를 올립니다.
> 공통 클라이언트는 [shared/apiclient](shared/apiclient/client.go), 계약은 [docs/web-api.md](docs/web-api.md).
> API 키·토큰은 사용자가 한 번만 입력하면 PC에 저장됩니다 (Windows DPAPI 암호화, [shared/secret](shared/secret/)).

## 프로그램 목록

| # | 이름 | 경로 | 상태 | 요약 |
|---|------|------|------|------|
| 1 | [PageVault](programs/PageVault/) | `programs/PageVault` | 구현 | 로그인된 Chrome 페이지를 주기적으로 SQLite에 백업하고 Web API로 업로드 |
| 2 | [NaverBookingScraper](programs/NaverBookingScraper/) | `programs/NaverBookingScraper` | 진행 중 | 네이버예약 스크랩: 파트너센터 예약을 수집해 Motospec Visit Import API로 등록·취소 (GUI·CLI) |
| 3 | [ActivityLogKeeper](programs/ActivityLogKeeper/) | `programs/ActivityLogKeeper` | 진행 중 | 웹 프로그램의 활동 로그 표를 행 단위로 로컬 SQLite에 보관하고 Web API로 업로드 (분쟁 대비, GUI·CLI) |
| 4 | [MoneyTrailScraper](programs/MoneyTrailScraper/) | `programs/MoneyTrailScraper` | 진행 중 | Playwright(Python)로 백그라운드 로그인해 정산·머니 내역 표를 SQLite에 쌓고 Web API로 업로드 (CLI) |

### 1. PageVault

Chrome 확장(MV3)이 지정한 URL을 주기적으로 열어 제목·본문·HTML을 읽고,
Native Messaging 호스트(`pagevault.exe`)가 로컬 SQLite에 저장한 뒤 Web API로 업로드합니다.

- **수집**: 이미 로그인된 Chrome 세션 사용, 주기 30초 이상, 로그인 만료 감지
- **저장**: 내용이 같으면 새 행 대신 확인 시각만 갱신, 서버가 끊겨도 다음에 재전송
- **제공 형태**: 트레이 앱·관리 창(`PageVault.exe`) · CLI(`pagevault.exe`) · Chrome 확장

```bash
cd programs/PageVault
make test     # 테스트
make build    # dist/PageVault-windows-amd64.zip
```

설계: [programs/PageVault/docs/PageVault_설계.md](programs/PageVault/docs/PageVault_설계.md)

### 2. NaverBookingScraper (네이버예약 스크랩, 진행 중)

Chrome 확장이 네이버 예약 파트너센터의 예약 목록을 읽고, 호스트(`naverbookingscraper.exe`)가
바뀐 예약만 Motospec Visit Import API(`/api/visit/import-external`)로 등록·취소합니다.
인증키(`msvi_…`)는 처음 한 번만 입력하면 저장됩니다.

```bash
cd programs/NaverBookingScraper
make test
naverbookingscraper api set    # 인증키 한 번 입력
```

설계·확인할 것: [programs/NaverBookingScraper/docs/설계.md](programs/NaverBookingScraper/docs/설계.md)

### 3. ActivityLogKeeper (활동 로그 보관, 진행 중)

Chrome 확장이 지정한 웹 프로그램의 활동 로그(이력 표)를 주기적으로 읽고,
호스트(`activitylogkeeper-cli.exe`)가 처음 보는 행만 로컬 SQLite에 쌓은 뒤 Web API로 올립니다.
서버가 끊기거나 상대 서비스의 기록이 바뀌어도 언제 무엇이 보였는지 PC에 남고, CSV로 내보낼 수 있습니다.

- **제공 형태**: 트레이 앱·관리 창(`ActivityLogKeeper.exe`) · CLI(`activitylogkeeper-cli.exe`) · Chrome 확장
- **관리 창**: 대상(URL·이름·선택자) 관리, 로그 조회·CSV 내보내기, 수집 기록, 주기·Web API·호스트 등록 설정
- **트레이**: 로그인 필요·표를 찾지 못한 대상이 생기면 알림, 지금 동기화

```bash
cd programs/ActivityLogKeeper
make test
make app      # Mac에서 트레이·관리 창 확인 (dist/ActivityLogKeeper)
make build    # dist/ActivityLogKeeper-windows-amd64.zip (GUI + CLI + 확장)
```

설명: [programs/ActivityLogKeeper/README.md](programs/ActivityLogKeeper/README.md)

### 4. MoneyTrailScraper (금융 내역 스크래퍼, 진행 중)

**Python + Playwright**로 헤드리스 Chromium을 띄워, 처음 한 번 입력한 아이디·비밀번호(DPAPI 암호화 저장)로 로그인하고
사이트 프로필(JSON)에 적은 정산·머니 내역 표를 긁어 옵니다. 세션을 저장해 재사용하고, 만료되면 다시 로그인합니다.
처음 보는 행만 SQLite에 쌓고 CSV로 내보내거나 Web API로 올립니다. 이 저장소에서 유일하게 Go가 아닌 프로그램입니다.

```bash
cd programs/MoneyTrailScraper
make setup && make test
.venv/bin/python -m moneytrail run <프로필>
```

설명: [programs/MoneyTrailScraper/README.md](programs/MoneyTrailScraper/README.md)

## 저장소 구조

```
AppWindows/
├── README.md                 # 프로그램 목록·요약
├── Makefile                  # make list / test / build — 각 프로그램 Makefile을 순회
├── go.work                   # 프로그램·공통 모듈을 묶는 Go 워크스페이스
├── docs/web-api.md           # Web API 계약 (모든 프로그램 공통)
├── shared/                   # 공통 Go 모듈 (appwindows/shared)
│   ├── apiclient/            # Web API 클라이언트: 인증, 배치 업로드, 오류 분류, 설정(api.json) 저장
│   └── secret/               # 키·토큰 보호 (Windows DPAPI)
└── programs/
    ├── ActivityLogKeeper/    # 활동 로그 보관 (진행 중)
    ├── MoneyTrailScraper/    # 금융 내역 스크래퍼 (Python + Playwright, 진행 중)
    ├── NaverBookingScraper/  # 네이버예약 스크랩 (진행 중)
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
2. `go.work`에 `use ./programs/<이름>`을 추가합니다 (`go work use ./programs/<이름>`).
   공통 모듈을 쓰면 `go.mod`에 `require appwindows/shared v0.0.0`과 `replace appwindows/shared => ../../shared`를 넣습니다.
3. `Makefile`에 `test`·`build` 타깃을 둡니다 (루트 `make test`/`make build`가 자동으로 실행).
4. 위 **프로그램 목록**에 한 줄 요약을 넣습니다.

GUI는 Wails v2를 CLI 없이 `go build -tags desktop,production -ldflags "-H windowsgui"`로 빌드하고,
exe 아이콘·매니페스트는 `go-winres`로 넣습니다 (PageVault `scripts/build.sh` 참고).
