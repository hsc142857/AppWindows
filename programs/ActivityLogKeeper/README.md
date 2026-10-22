# ActivityLogKeeper (활동 로그 보관)

웹 프로그램의 **활동 로그(이력 표)** 화면을 로그인된 Chrome에서 주기적으로 읽어
**행 단위로 로컬 SQLite에 먼저 저장**하고, 설정하면 **Web API로 업로드**하는 Windows 프로그램입니다.
서버 장애·상대 서비스의 기록 변경이 있어도 "언제 어떤 기록이 보였는지"를 PC에 남겨 분쟁 대응 자료로 씁니다.

> 상태: 코어(저장·중복 제거·업로드·CSV 내보내기), CLI·Native Messaging 호스트, Chrome 확장, 트레이·관리 창(Wails) 구현.
> 업로드 서버(`/api/v1/activitylog/entries:batch`)는 아직 없습니다 — [docs/web-api.md](../../docs/web-api.md).

## 동작

```
Chrome 확장 (알람, 기본 10분)
  └ 대상 URL을 최소화 창에서 열기 → 표 읽기 (모든 프레임, 행이 가장 많은 표 / 지정 선택자)
      └ Native Messaging → activitylogkeeper-cli (호스트)
          ├ captures: 수집 기록 한 줄 (상태·행 수·새 행 수·시각)
          ├ entries : 처음 보는 행만 추가, 이미 있는 행은 last_seen_at 갱신
          └ sync    : 미전송 행을 100건씩 업로드 (실패하면 남겨 두고 다음 라운드에 재전송)
```

- **행 식별**: 공백을 정리한 셀 내용의 SHA-256(`row_hash`). 한 화면에 같은 행이 여러 개면 순번(`occurrence`)으로 구분합니다.
- **uid**: `sha256(대상 URL, row_hash, occurrence)` 앞 16바이트 — 다시 보내거나 다른 PC에서 보내도 같은 uid라 서버에서 중복되지 않습니다.
- **상태**: `ok` · `empty`(표를 찾지 못함 → 선택자 확인) · `login_required`(로그인 페이지로 이동) · `error`.
- 대상을 지워도 모은 로그·수집 기록은 지우지 않습니다 (증빙).

## 표 고르기 (선택자)

| 선택자 | 읽는 방식 |
|---|---|
| (비움) | 페이지의 `<table>`·`role=grid/table` 중 **행이 가장 많은 것** |
| 요소 하나에 맞음 | 표면 그 표, ARIA 표면 `role=row`, 아니면 안의 `<table>` 또는 자식 요소를 행으로 |
| 요소 여러 개에 맞음 | 맞은 요소 하나하나를 행으로 (자식 요소가 셀) — 예: `.log-list > li` |

표가 늦게 그려지는 화면(SPA)은 행이 0이면 2초씩 3번 다시 봅니다. iframe 안의 표도 읽습니다.

## 트레이 · 관리 창 (ActivityLogKeeper.exe)

| 실행 | 내용 |
|---|---|
| `ActivityLogKeeper.exe` | 알림 영역 상주: 대상·로그·미전송 수, 마지막 수집, **로그인 필요 / 표 못 찾음** 대상이 새로 생기면 Windows 알림, 지금 동기화 |
| `ActivityLogKeeper.exe --window` | 관리 창 (트레이 클릭으로도 열림) |

관리 창 탭:

- **개요** — 요약, 정상이 아닌 대상, 서버 거부 행 다시 보내기, 지금 동기화
- **대상** — URL·이름·CSS 선택자 추가, 수정(이름·선택자), 사용 여부, 삭제, 대상별 로그 보기
- **로그** — 대상·기간(처음 확인 날짜) 필터로 최근 행 보기, **CSV 내보내기**(같은 조건 전체, 저장 위치 선택)
- **수집 기록** — 언제 몇 행이 보였는지(captures), 상태·새 행 수
- **설정** — 수집 주기·로딩 대기, Web API 주소/토큰(DPAPI 저장, 접속 테스트), Chrome 호스트 등록

트레이와 관리 창은 별도 프로세스이며 호스트와 같은 SQLite(WAL)를 씁니다. core 패키지만 호출합니다.

## CLI

```
activitylogkeeper-cli status
activitylogkeeper-cli target add <url> [이름] [--selector <CSS>]
activitylogkeeper-cli target list | selector <id> [CSS] | rename <id> <이름> | remove|enable|disable <id>
activitylogkeeper-cli interval [초] | delay [초]
activitylogkeeper-cli captures [개수] [--url <url>]
activitylogkeeper-cli entries [개수] [--url <url>]
activitylogkeeper-cli export [--url <url>] [--from YYYY-MM-DD] [--to YYYY-MM-DD] [--out 파일.csv]
activitylogkeeper-cli api set <주소> [토큰] | show | test | clear
activitylogkeeper-cli sync [--retry-rejected]
activitylogkeeper-cli install-host [확장ID...] | uninstall-host
```

| 항목 | 값 |
|---|---|
| 호스트 이름 | `com.appwindows.activitylogkeeper` |
| 확장 ID | `bokkckkphfafbndhkgncoledplffljih` (manifest `key`로 고정) |
| 데이터 위치 | `%LOCALAPPDATA%\ActivityLogKeeper` (`ACTIVITYLOGKEEPER_HOME`으로 변경) |
| API | `POST /api/v1/activitylog/entries:batch` (공통 배치 계약) |

```bash
make test      # 코어 테스트
make vet       # Mac·Windows 정적 검사 (desktop,production 태그)
make cli       # Mac CLI: dist/activitylogkeeper-cli
make app       # Mac 트레이·관리 창: dist/ActivityLogKeeper (--window로 창만)
make build     # Windows: dist/ActivityLogKeeper-windows-amd64.zip (GUI + CLI + extension + README.txt)
make icons     # 아이콘 다시 그리기 (extension/icons, app/assets)
```

확장 표 추출기는 [testdata/activity.html](testdata/activity.html)로 브라우저에서 확인합니다.

## 구조

```
ActivityLogKeeper/
├── core/
│   ├── paths.go         # 데이터 위치, 호스트 이름, 확장 ID
│   ├── models.go        # Target · Capture · Entry, 셀 정리
│   ├── store.go         # SQLite: targets / captures / entries(아웃박스) / settings
│   ├── api.go           # Web API 업로드 (shared/apiclient)
│   ├── export.go        # CSV 내보내기 (UTF-8 BOM, Excel용)
│   ├── nativemsg.go     # Chrome Native Messaging: ping / config / save / sync
│   ├── login.go         # 로그인 페이지 이동 감지
│   └── hostinstall*.go  # 호스트 등록 (Windows 레지스트리 / Mac·Linux 매니페스트)
├── app/                    # ActivityLogKeeper.exe (Wails v2)
│   ├── main.go             # 트레이 / --window 분기, 아이콘·프런트엔드 embed
│   ├── tray.go             # 알림 영역 메뉴·알림 (fyne systray, beeep)
│   ├── window.go           # 관리 창 (Wails, 단일 인스턴스)
│   ├── api.go              # 프런트엔드 바인딩 (core 호출)
│   ├── single_*.go         # 트레이 중복 실행 방지 (Windows 뮤텍스)
│   ├── assets/             # icon.png · icon.ico
│   └── frontend/dist/      # 관리 창 HTML/CSS/JS (빌드 도구 없음)
├── cmd/activitylogkeeper/  # CLI · 호스트 (배포 이름 activitylogkeeper-cli)
├── extension/              # Chrome 확장 (MV3): background.js(수집·표 추출), popup, icons
├── scripts/build.sh        # Windows 배포 zip (go-winres 아이콘·매니페스트)
├── scripts/genicon/        # 아이콘 생성
└── docs/사용법.txt          # 배포본 README.txt
```

## 확인할 것

- 업로드 서버: 공통 배치 계약대로 새로 만들지, 기존 서버(Motospec 등)에 엔드포인트를 둘지
- 대상 웹 프로그램의 로그 화면이 페이지를 나눠 보여 주면(페이지네이션) 첫 페이지만 읽습니다 — 한 화면에 많이 보이는 URL 사용 권장
- "3분 전" 같은 상대 시각 열은 매번 새 행이 됩니다 — 필요하면 무시할 열 설정 추가
