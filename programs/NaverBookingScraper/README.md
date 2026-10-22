# NaverBookingScraper (네이버예약 스크랩)

네이버 예약 파트너센터의 **예약 목록을 수집**해 로컬 SQLite에 보관하고,
**Motospec Visit Import API**로 등록·취소하는 Windows 프로그램입니다.

> 상태: Visit Import API v1.3.0 연동, 인증키 저장, **전송 현황(전송 완료 / 미전송)**, GUI 관리 창·CLI 구현. 수집(확장 파서)·트레이 상주는 예정. 설계는 [docs/설계.md](docs/설계.md).

## 실행 (GUI)

```bash
make app                                   # Mac: dist/NaverBookingScraper.app (더블클릭 또는 open)
make build                                 # Windows: dist/NaverBookingScraper-windows-amd64.zip (GUI + CLI + 샘플 + README.txt)
```

- 처음 실행하면 **인증키 설정 화면**이 나옵니다. 키를 확인한 뒤 저장하고, 다음부터는 묻지 않습니다 (CLI와 같은 키를 씁니다).
- 상단에 인증키를 발급한 **업체명**이 보이고, 업체가 바뀌면 경고 띠가 나옵니다.
- 탭: **전송 현황** · **서버 예약**(날짜별) · **예약 찾기**(예약번호) · **가져오기**(JSON = 긁어 온 데이터 대신) · **설정**
- **전송 현황**: 긁어 온 예약은 저장 후 바로 전송되고, **서버가 처리한 것만 "전송 완료"**에 나옵니다.
  안 간 예약은 **"미전송"**에 따로(대기 · 전송 실패 · 거부 · 다른 업체) 사유와 함께 나옵니다.
  전송 실패는 1분마다 자동 재시도, 거부는 "거부 건 다시 전송"으로 다시 보냅니다.

## 인증키

Motospec 탑바 «인증키»에서 발급한 `msvi_…` 키를 **처음 한 번만** 입력하면 됩니다.

```bash
naverbookingscraper-cli api set            # 주소(기본 http://localhost:3000)와 인증키 입력 → 연결 확인 후 저장
naverbookingscraper-cli api test           # 저장된 키로 연결 테스트
```

- 입력한 키는 연결 테스트(GET)로 확인한 뒤 저장하고, 틀리면 저장하지 않습니다.
- `%LOCALAPPDATA%\NaverBookingScraper\api.json`에 저장되며 Windows에서는 DPAPI로 암호화됩니다 (같은 사용자만 읽음).
- 키가 없을 때 키가 필요한 명령을 실행하면 그 자리에서 한 번 묻습니다. 이후에는 묻지 않습니다.
- 여러 업체가 같은 프로그램을 쓰므로, 실행할 때 **인증키를 발급한 업체명**을 서버에서 받아 보여 줍니다
  (`naverbookingscraper-cli status`). 다른 업체의 키로 바뀌면 경고합니다.

## 구조

```
NaverBookingScraper/
├── go.mod                      # module naverbookingscraper (+ appwindows/shared)
├── Makefile                    # test / vet / build
├── core/
│   ├── paths.go                # 데이터 위치, 호스트 이름, 확장 ID, 기본 API 주소
│   ├── config.go               # 인증키 저장·불러오기 (shared/apiclient + shared/secret)
│   ├── partner.go              # 인증키 발급 업체 확인·기억, 업체 바뀜 경고
│   ├── visitapi.go             # Visit Import API v1.3.0: Ping / ListDay / GetVisit / Import(200건씩, 사전 검증, dryRun)
│   ├── models.go               # 네이버 Reservation → ImportItem 변환·검증
│   └── store.go                # 로컬 SQLite 아웃박스: 저장 → 전송 → 전송 완료 / 미전송
├── cmd/naverbookingscraper/    # CLI · Native Messaging 호스트 (배포 이름 naverbookingscraper-cli)
├── scripts/build-mac-app.sh    # Mac .app 번들 (로컬 테스트용, 서명 없음)
├── scripts/build-windows.sh    # Windows 배포 zip (아이콘·버전·DPI 매니페스트, README.txt = docs/사용법.txt)
├── app/                        # GUI 관리 창 (Wails) — main.go, api.go(바인딩), frontend/dist(HTML/JS)
├── extension/                  # Chrome 확장 — ID dabccgindpajbmnhcbfemdnngddecmdh 고정
│   ├── manifest.json           # host_permissions: partner.booking.naver.com
│   ├── background.js           # 수집 라운드 (골격)
│   └── parse.js                # 예약 목록 파서 (골격)
└── docs/설계.md
```

## CLI

```
naverbookingscraper-cli status                     # 실행 확인: 서버 연결·인증키·발급 업체
naverbookingscraper-cli api set [주소] | show | test | clear
naverbookingscraper-cli visits [날짜]              # 서버에 저장된 그날 예약 (YYYY-MM-DD, 생략 시 오늘)
naverbookingscraper-cli visit <예약번호>             # 서버에 저장된 예약 한 건
naverbookingscraper-cli import <예약.json> [--dry-run]   # 저장 후 전송 (GUI 전송 현황에 나옴), --dry-run: 저장·전송 없이 예상 결과만
```

| 항목 | 값 |
|---|---|
| 호스트 이름 | `com.appwindows.naverbookingscraper` |
| 데이터 위치 | `%LOCALAPPDATA%\NaverBookingScraper` (`NAVERBOOKINGSCRAPER_HOME`으로 변경) |
| API | `GET`·`POST /api/visit/import-external` |

```bash
make test
make app     # Mac GUI: dist/NaverBookingScraper.app
make cli     # Mac CLI: dist/naverbookingscraper-cli
make build   # Windows 배포 zip: dist/NaverBookingScraper-windows-amd64.zip
```

Mac·Windows 테스트 절차와 확인 목록: [docs/테스트.md](docs/테스트.md)
