# Web API 계약

AppWindows 프로그램은 **DB에 직접 연결하지 않고** HTTP API로 데이터를 올린다.
서버는 이 저장소 밖에서 만든다. 클라이언트 구현은 [`shared/apiclient`](../shared/apiclient/client.go).

| 프로그램 | API | 문서 |
|---|---|---|
| PageVault | 이 문서의 공통 배치 계약 (`/api/v1/pagevault/snapshots:batch`) — **초안, 서버 미구현** | 아래 |
| ActivityLogKeeper | 공통 배치 계약 (`/api/v1/activitylog/entries:batch`) — **초안, 서버 미구현** | 아래 |
| MoneyTrailScraper | 공통 배치 계약 (`/api/v1/moneytrail/entries:batch`) — **초안, 서버 미구현** | [README](../programs/MoneyTrailScraper/README.md#web-api-업로드) |
| NaverBookingScraper | **Motospec Visit Import API** (`/api/visit/import-external`) — 실제 서버 있음 | [설계 문서](../programs/NaverBookingScraper/docs/설계.md#visit-import-api-motospec) |

> 이미 정해진 외부 API(Motospec 등)는 그 명세를 따르고, 새로 만드는 API는 아래 공통 형식을 기본으로 한다.

## 공통

| 항목 | 규칙 |
|---|---|
| 주소 | 프로그램마다 설정하는 `baseURL` (예: `https://api.example.com`). https만, 개발용 localhost는 http 허용 |
| 인증 | `Authorization: Bearer <토큰>` — 사용자가 한 번 입력하면 `api.json`에 저장 (Windows DPAPI 암호화, `apiclient.SaveConfig`) |
| 형식 | 요청·응답 모두 `application/json; charset=utf-8` |
| 시각 | ISO8601 문자열. 수집 시각은 UTC(`Z`), 원문 시각은 원래 시간대 표기 유지 |
| User-Agent | `AppWindows-<프로그램>/<버전>` |
| 타임아웃 | 클라이언트 30초 |

### 오류 응답

2xx가 아니면 본문은 `{"error": "설명"}`.

| 상태 | 의미 | 클라이언트 동작 |
|---|---|---|
| 400 | 요청 형식 오류 | 재시도하지 않음, 로그·화면에 표시 |
| 401 / 403 | 토큰 오류·권한 없음 | 재시도하지 않음, "API 설정 확인" 알림 |
| 413 | 배치가 너무 큼 | 배치 크기 확인 (최대 100) |
| 429 / 5xx | 과부하·서버 오류 | 데이터는 로컬에 남기고 다음 주기에 재전송 |

## 배치 업로드 (모든 수집 프로그램 공통 형식)

```
POST /api/v1/<자원>:batch
{
  "sourceHost": "PC 이름",
  "items": [ { "uid": "…", … }, … ]      // 최대 100개
}

200 OK
{
  "accepted": ["uid", …],                 // 저장했거나 이미 있던 항목 (멱등)
  "rejected": [{ "uid": "…", "error": "…" }]   // 항목 단위 검증 실패
}
```

- **멱등성**: 서버는 `uid`가 이미 있으면 새로 만들지 않고 accepted로 돌려준다.
  → 네트워크 오류로 응답을 못 받아 다시 보내도 중복되지 않는다.
- 클라이언트는 accepted에 든 uid만 "업로드 완료"로 표시한다. 나머지는 로컬 SQLite(outbox)에 남는다.

## 엔드포인트

| 메서드·경로 | 프로그램 | 항목 모델 |
|---|---|---|
| `GET /api/v1/health` | 공통 | 응답 `{"ok": true, "version": "…"}` — 접속·토큰 확인용 |
| `POST /api/v1/pagevault/snapshots:batch` | PageVault | [`SnapshotItem`](../programs/PageVault/core/api.go) — 최대 100건·약 8MB씩 |
| `POST /api/v1/activitylog/entries:batch` | ActivityLogKeeper | [`EntryItem`](../programs/ActivityLogKeeper/core/api.go) — 최대 100건씩 |
| `POST /api/v1/moneytrail/entries:batch` | MoneyTrailScraper | [`entry_item`](../programs/MoneyTrailScraper/moneytrail/api.py) — 최대 100건씩 |

### pagevault/snapshots

- 같은 uid는 같은 스냅샷이다. 다시 오면 `lastSeenAt`만 갱신한다 (내용이 같은 페이지를 다시 확인한 것).
- `status`가 `ok`가 아니면 `text`·`html`은 null.

### activitylog/entries

활동 로그 화면의 표 한 행이 항목 하나다.

```json
{
  "uid": "3f0c…(32자 hex)",
  "targetURL": "https://app.example.com/activity",
  "targetName": "업무앱",
  "headers": ["일시", "작업", "사용자"],
  "cells": ["2026-10-19 09:12", "결제 승인", "lee"],
  "rowHash": "…(64자 hex, 정리한 셀 내용의 SHA-256)",
  "occurrence": 1,
  "firstSeenAt": "2026-10-19T00:12:30.000000Z"
}
```

- uid는 `targetURL`·`rowHash`·`occurrence`로 정해진다. 같은 행은 다시 보내거나 다른 PC에서 보내도 같은 uid다.
- 한 번 올린 행은 다시 보내지 않는다 (마지막 확인 시각은 PC에만 기록).

### moneytrail/entries

금융 내역 화면(정산·머니 내역 등)의 표 한 행이 항목 하나다.

```json
{
  "uid": "…(32자 hex)",
  "profile": "example-pay",
  "pageName": "정산내역",
  "pageURL": "https://partner.example.com/settlements",
  "headers": ["정산일", "구분", "금액"],
  "cells": ["2026-10-01", "카드 정산", "120,000"],
  "rowHash": "…(64자 hex)",
  "occurrence": 1,
  "firstSeenAt": "2026-10-22T09:07:30.197438Z"
}
```

- uid는 `profile`·`pageName`·`rowHash`·`occurrence`로 정해진다. 금액·날짜는 화면 문자열 그대로 보낸다 (서버에서 해석).

## 확인할 것

- PageVault·ActivityLogKeeper 업로드를 받을 서버: 이 공통 계약대로 새로 만들지, Motospec 쪽에 엔드포인트를 둘지
- 실제 서버 주소, 토큰 발급·만료 방식
- 요청 본문 최대 크기 (PageVault는 HTML 때문에 약 8MB로 자른다)
