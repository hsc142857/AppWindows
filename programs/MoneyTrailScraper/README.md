# MoneyTrailScraper

정산 내역, 머니 내역 같은 **금융 거래 내역 표**를 **Playwright(Python)**로 긁어 오는 프로그램입니다.
백그라운드(헤드리스) 브라우저로 로그인한 뒤 로컬 SQLite에 쌓고, 설정하면 Web API로 올립니다.

AppWindows 프로그램 목록의 4번 항목입니다. 전체 목록은 [저장소 README](../../README.md)를 보세요.
다른 프로그램과 달리 Go가 아니라 **Python**입니다. Chrome 확장 없이 Playwright가 브라우저를 직접 띄웁니다.

## 흐름

```
moneytrail run <프로필>
  └ 헤드리스 Chromium 실행 (저장된 세션 쿠키가 있으면 불러옴)
  └ 대상 페이지 열기 → 로그인 화면이면 저장된 아이디·비밀번호로 로그인 → 다시 열기
       (처음 실행이면 아이디·비밀번호를 물어보고 저장)
  └ 표의 행 추출 → "다음" 버튼이 있으면 maxPages까지 넘기며 반복
  └ 세션 저장 → SQLite(처음 보는 행만 추가) → Web API 업로드(설정했을 때)
```

| 파일 | 내용 |
|---|---|
| `moneytrail/scraper.py` | Playwright: 로그인 감지·로그인, 표 추출, 페이지 넘김 |
| `moneytrail/profile.py` | 사이트 프로필(JSON) 검증 |
| `moneytrail/credentials.py` | 아이디·비밀번호, 세션 저장 (Windows DPAPI 암호화) |
| `moneytrail/store.py` | SQLite (`entries`, `runs`) |
| `moneytrail/api.py` | Web API 배치 업로드 (`docs/web-api.md` 공통 계약) |
| `moneytrail/cli.py` | CLI |
| `profiles/example.json` | 프로필 예시 |
| `tests/` | 가짜 금융 사이트(`fakesite.py`)에 실제로 로그인하는 통합 테스트 포함 |

## 설치·테스트

```bash
make setup    # .venv + Playwright + Chromium (처음 한 번)
make test
```

Windows에서는 Python 3.9 이상을 설치한 뒤 같은 순서로 진행합니다.

```bat
py -m venv .venv
.venv\Scripts\python -m pip install -r requirements.txt
.venv\Scripts\python -m playwright install chromium
```

## 사용법

```bash
alias moneytrail='.venv/bin/python -m moneytrail'   # Windows: .venv\Scripts\python -m moneytrail

moneytrail profile add profiles/example.json   # 프로필 등록 (선택자를 실제 사이트에 맞게 고친 뒤)
moneytrail run example-pay                      # 처음이면 아이디·비밀번호를 묻고 저장, 이후엔 자동
moneytrail entries example-pay -n 20
moneytrail export example-pay 내역.csv          # 페이지가 여러 개면 내역_정산내역.csv, 내역_머니내역.csv
```

```
moneytrail profile add <파일.json> | list | show <이름>
moneytrail login set <프로필> | test <프로필> [--show] | clear <프로필>
moneytrail run <프로필> [--page 이름] [--show] [--no-sync]
moneytrail entries [<프로필>] [--page 이름] [-n 20]
moneytrail export <프로필> <파일.csv> [--page 이름]
moneytrail api set <https://…> [토큰] | show | test | clear
moneytrail sync [--retry-rejected]
moneytrail status
```

- `--show`: 브라우저 창을 띄웁니다. 2단계 인증이나 캡차가 나오면 창에서 직접 끝내면 됩니다 (최대 3분 대기). 끝나면 세션이 저장되어 다음부터는 백그라운드로 돌아갑니다.
- 종료 코드: `0` 성공, `1` 일부 페이지 실패·오류, `2` 로그인 실패. 작업 스케줄러에 걸 때 씁니다.

## 프로필 (사이트마다 JSON 하나)

```json
{
  "name": "example-pay",
  "title": "예시 페이",
  "login": { "url": "…/login", "username": "#loginId", "password": "#loginPw",
             "submit": "button[type=submit]", "success": "", "error": ".error-message" },
  "pages": [
    { "name": "정산내역", "url": "…/settlements", "rows": "table tbody tr", "next": "button.next", "maxPages": 20 },
    { "name": "머니내역", "url": "…/money", "waitFor": "ul.history", "rows": "ul.history > li",
      "columns": { "일시": ".date", "금액": ".amount" } }
  ]
}
```

| 항목 | 설명 |
|---|---|
| `login.username` / `password` / `submit` | 입력칸·버튼 CSS 선택자. `submit`이 없으면 비밀번호 칸에서 Enter |
| `login.success` | 로그인 성공 후 보이는 요소. 비우면 "로그인 주소를 벗어나면 성공" |
| `login.error` | 실패 메시지 요소. 있으면 실패 사유로 보여 줍니다 |
| `pages[].rows` | 행 선택자 |
| `pages[].columns` | `{열 이름: 행 안의 선택자}`. 비우면 표의 `thead th`를 머리글로, 행의 모든 `td`를 값으로 씁니다 |
| `pages[].waitFor` | JS로 늦게 그려지는 화면이면 이 요소가 나타날 때까지 기다립니다 (기본 `rows`) |
| `pages[].next` / `maxPages` | 다음 페이지 버튼과 최대 쪽 수 (1~200). 버튼이 없거나 비활성이면 멈춥니다 |

로그인 화면 판단: 대상 페이지를 열었는데 로그인 주소로 이동했거나 `login.password` 입력칸이 보이면 로그인합니다.
그래서 세션이 만료되면 저절로 다시 로그인합니다.

## 데이터

`%LOCALAPPDATA%\MoneyTrailScraper\` (Mac: `~/Library/Application Support/MoneyTrailScraper/`, `MONEYTRAIL_HOME`으로 변경 가능)

| 파일 | 내용 |
|---|---|
| `moneytrail.sqlite` | `entries`(행), `runs`(실행 기록) |
| `credentials.json` | 프로필별 아이디·비밀번호 — **Windows DPAPI 암호화**, 파일 권한 0600 |
| `sessions/<프로필>.json` | 로그인 세션(쿠키·localStorage) — DPAPI 암호화 |
| `profiles/<프로필>.json` | 등록한 프로필 |
| `api.json` | Web API 주소·토큰 (`shared/apiclient`와 같은 형식) |

- 행 uid = SHA-256(프로필, 페이지, 행 내용, 같은 내용 안의 순번)의 앞 32자입니다. 같은 행을 다시 긁으면 `last_seen_at`만 갱신합니다.
  같은 내용의 행이 한 화면에 두 번 나오면(같은 날 같은 금액 정산 등) 순번 1, 2로 따로 저장합니다.
- Mac·Linux(개발용)에서는 DPAPI가 없어 비밀값이 평문이며 파일 권한 0600으로만 보호됩니다 (`status`에 표시).

## Web API 업로드

`POST /api/v1/moneytrail/entries:batch` — [공통 배치 계약](../../docs/web-api.md), 100건씩, **초안이며 서버는 아직 없습니다**.
항목: `uid, profile, pageName, pageURL, headers, cells, rowHash, occurrence, firstSeenAt`.
429·5xx·접속 실패면 남은 행은 다음 실행에 다시 보내고, 거부된 행은 `sync --retry-rejected`로 다시 보냅니다.

## 알려진 한계

- **키보드 보안 모듈, 가상 키패드, 공동·금융인증서를 쓰는 은행·카드사 로그인은 자동으로 되지 않습니다.**
  그런 사이트는 `--show`로 직접 로그인해 세션을 저장해 쓰고, 세션이 만료되면 다시 해야 합니다.
- 2단계 인증·캡차는 자동으로 풀지 않습니다 (`--show`로 직접 처리).
- CSS 선택자 기반이라 사이트 화면이 바뀌면 프로필을 고쳐야 합니다. 행이 0건이면 선택자부터 확인하세요.
- 자동 로그인·수집을 금지하는 약관이 있는 사이트가 있습니다. 쓰기 전에 해당 서비스 약관을 확인하세요.
- 주기 실행은 내장하지 않았습니다. Windows 작업 스케줄러에 `run`을 등록해 씁니다.
- 아직 단일 exe 배포본과 GUI가 없습니다 (Python과 가상환경이 필요).
