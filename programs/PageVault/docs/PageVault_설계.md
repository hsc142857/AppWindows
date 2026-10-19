# PageVault 설계 (Windows)

macOS용으로 설계하던 것을 Windows 저장소(AppWindows)로 옮겨 **Go**로 구현했다.
(원래 권장안은 C#/.NET 8이었으나, Mac에서 개발·크로스 빌드하기 위해 Go + Wails로 정했다.)

## 요구사항

- 수집 대상: 지정한 URL 목록 (예: 네이버 예약 목록, 다른 사이트의 기록 로그). 이미 로그인된 Chrome 세션을 쓴다.
- Chrome 접근: 확장 프로그램(Manifest V3) + Native Messaging.
- 저장 형태: 페이지 스냅샷(제목·본문 텍스트·HTML 통째). 사이트별 파서는 만들지 않는다.
- 실행 시점: 주기적 자동 수집, 주기는 초 단위 (chrome.alarms 최소 간격 때문에 최소 30초).
- DB: SQLite에 먼저 저장 → 원격 Postgres로 동기화. Postgres가 끊겨도 유실 없이 다음에 재전송.
- 형태: CLI + 트레이 앱.

## 기술 선택

| 항목 | 선택 | 이유 |
|---|---|---|
| 언어 | Go | Mac에서 Windows exe로 크로스 빌드 (cgo 없음) |
| SQLite | `modernc.org/sqlite` | 순수 Go (cgo 불필요) |
| Postgres | `jackc/pgx/v5` | 배치 upsert, 접속 타임아웃 |
| 트레이 | `fyne.io/systray` | Windows 구현이 순수 Go |
| 관리 창 | Wails v2 (WebView2) | Mac에서도 화면 확인 가능, CLI 없이 빌드 |
| 알림 | `gen2brain/beeep` | Windows 토스트 알림 |
| 비밀번호 보호 | DPAPI (`x/sys/windows`) | 현재 사용자만 복호화 |

트레이와 관리 창은 각자 메시지 루프를 쓰므로 **별도 프로세스**다 (`PageVault.exe` / `PageVault.exe --window`).
트레이는 이름 있는 뮤텍스로, 관리 창은 Wails SingleInstanceLock으로 중복 실행을 막는다.

## 흐름

```
Chrome 확장 (MV3)
  └ chrome.alarms 주기마다 → connectNative("com.pagevault.host")로 호스트 실행
  └ config 요청 → 대상 URL 목록·주기·로딩 대기 수신 (주기 바뀌면 알람 재설정)
  └ 최소화된 새 창에서 URL을 하나씩 열기
  └ 로딩 완료(status complete, 60초 타임아웃) + loadDelay초 대기 → executeScript로
     {title, location.href, body.innerText, documentElement.outerHTML} 추출
  └ save 요청으로 하나씩 전송 → 끝나면 sync 요청 → 창 닫고 포트 종료
  └ 라운드 중복 실행 방지(chrome.storage.session), 팝업에 "지금 수집"과 마지막 결과
        ↓ Native Messaging (stdin/stdout)
pagevault.exe (native-host 모드) → SQLite 저장 → Postgres 동기화
        ↑
PageVault.exe: 대상 URL 관리, 주기·로딩 대기, 최근 스냅샷/상태, 로그인 만료 알림,
               Postgres 설정·접속 테스트, 지금 동기화, 호스트 등록
```

- Native Messaging은 Chrome이 필요할 때 호스트를 직접 실행하므로 트레이 앱이 꺼져 있어도 수집된다.
  localhost 포트를 열 필요도 없다.
- URL 목록의 유일한 원본은 SQLite `targets` 테이블. 확장은 라운드마다 호스트에 물어본다.

## Native Messaging 프로토콜

- 4바이트 길이(리틀 엔디언 uint32) + UTF-8 JSON. Chrome→호스트 최대 64MiB, 호스트→Chrome 최대 1MB.
- stdout은 통신 전용, 로그는 `host.log`(5MB 넘으면 `host.log.1`로 교체)에만 쓴다.
  Go는 Windows에서도 stdin/stdout을 텍스트 변환 없이 다룬다.
- Chrome이 넘기는 `chrome-extension://<id>/` 인자로 native-host 모드에 들어간다.
- 요청 `{id, type, capture?}` / 응답 `{id, ok, error?, ...}`
  - `ping` → `{version}`
  - `config` → `{intervalSeconds, loadDelaySeconds, targets:[{id,url,name}]}`
  - `save` + `capture:{targetID, requestedURL, finalURL, title, text, html, error}` → `{snapshotID, status, changed}`
  - `sync` → `{postgresConfigured, sent, remaining}` (실패하면 ok:false + error, 데이터는 SQLite에 남음)

## 호스트 등록

`pagevault install-host [확장ID...]` 또는 관리 창의 "Chrome 호스트 등록":
실행 파일을 `%LOCALAPPDATA%\PageVault\bin\pagevault.exe`로 복사하고, 매니페스트 JSON
(name `com.pagevault.host`, type stdio, path, allowed_origins)을 쓴 뒤
`HKCU\Software\Google\Chrome\NativeMessagingHosts\com.pagevault.host` 기본값에 경로를 등록한다.
실행 중인 exe는 덮어쓸 수 없으므로 기존 파일은 `.old`로 이름을 바꾼 뒤 교체한다.
(Mac·Linux 개발 환경에서는 Chrome 사용자 폴더의 `NativeMessagingHosts`에 매니페스트를 둔다.)

## SQLite 스키마

- WAL, busy_timeout 5000, foreign_keys.
- `targets(id, url UNIQUE, name, enabled, created_at)` — http(s)만, 중복 거부.
- `snapshots(id, uid UNIQUE, target_id FK ON DELETE SET NULL, target_url, target_name, final_url,
  title, text, html, content_hash, status, error, captured_at, last_seen_at, synced_at)`
  인덱스 `(target_url, id)`, 미동기화 부분 인덱스 `WHERE synced_at IS NULL`.
- `settings(key, value)` — `interval_seconds`(기본 3600, 최소 30), `load_delay_seconds`(기본 3, 0~120).
- 시각은 고정 폭 ISO8601 UTC 문자열 `2006-01-02T15:04:05.000000Z`.

## 저장 규칙

- status: `error`(캡처 오류) / `login_required`(로그인 페이지로 리다이렉트) / `ok`.
- 로그인 리다이렉트: 요청 URL과 최종 URL이 다르고(#·끝 슬래시 무시), 최종 호스트가
  nid.naver.com, accounts.google.com, accounts.kakao.com, logins.daum.net, login.microsoftonline.com,
  login.live.com, appleid.apple.com 중 하나이거나 경로 조각이 login/signin/sign-in/sign_in/logon/auth
  (또는 그 접두 + ".", nidlogin…)이면 login_required. 대상 URL 자체가 로그인 페이지면 아님.
- ok가 아니면 text/html은 저장하지 않는다.
- content_hash: ok는 SHA-256(title + 0x1F + text) (HTML은 매번 바뀌는 토큰 때문에 제외),
  login_required는 고정값, error는 오류 메시지 기준.
- 같은 target_url의 직전 스냅샷과 status·hash가 같으면 새 행 대신 last_seen_at 갱신 + synced_at=NULL.

## Postgres 동기화

- 접속 정보: `postgres://user:pass@host:5432/db?sslmode=disable|prefer|require` + 테이블 이름
  (기본 `pagevault_snapshots`, `schema.table` 허용, 영문·숫자·밑줄만 — 검증 후 식별자 인용).
- `postgres.json`(0600)에 저장. URL에서 비밀번호를 떼어 Windows는 DPAPI로 암호화해 따로 저장.
  표시할 때는 `****`. 관리 창에서 `****`를 그대로 저장하면 기존 비밀번호를 유지한다. 접속 타임아웃 10초.
- 테이블 자동 생성: uid uuid PK, target_url, target_name, final_url, title, text, html, content_hash,
  status, error, captured_at, last_seen_at, source_host(PC 이름), synced_at default now().
  인덱스 `(target_url, captured_at)`.
- 미동기화 행을 100개씩 한 트랜잭션으로 `ON CONFLICT (uid) DO UPDATE SET last_seen_at, synced_at` upsert.
- 완료 표시는 `WHERE id=? AND last_seen_at=<보낸 값>` — 보내는 사이 갱신된 행은 다음에 재전송.

## 테스트

`core/*_test.go`: 메시지 프레이밍, 호스트 요청 처리, 중복 저장 방지, 로그인 감지, 설정 검증,
URL·테이블 이름 검증, Postgres 설정 저장/로드, 동기화 완료 표시 조건.
`PAGEVAULT_TEST_PG`가 있으면 실제 Postgres로 250건 배치 전송·재전송까지 검증한다.

## 알려진 한계

- 수집하는 동안 Chrome이 켜져 있어야 한다 (꺼져 있던 시간은 수집 안 됨).
- 확장은 처음 한 번 "압축해제된 확장 프로그램 로드"로 설치.
- 주기를 줄이면 다음 라운드부터 적용.
