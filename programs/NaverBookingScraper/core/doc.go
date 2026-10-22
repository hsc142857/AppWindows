// Package core는 NaverBookingScraper의 로직이다. CLI(cmd/naverbookingscraper)와 트레이 앱(app)이 함께 쓴다.
//
// 데이터 흐름:
//
//	Chrome 확장(네이버 예약 파트너센터) → (Native Messaging) → 호스트 → SQLite(outbox) → Visit Import API
//
// DB에 직접 연결하지 않는다. 서버 저장은 Motospec Visit Import API(POST /api/visit/import-external)가 맡는다.
//
// 파일 구성 (✓ 구현, … 예정):
//
//	✓ paths.go      데이터 위치 (%LOCALAPPDATA%\NaverBookingScraper, NAVERBOOKINGSCRAPER_HOME)
//	✓ config.go     API 주소·인증키 저장 (한 번 입력, Windows DPAPI 암호화)
//	✓ partner.go    인증키 발급 업체 확인·기억 (실행 시 CheckPartner, 업체가 바뀌면 경고)
//	✓ visitapi.go   Visit Import API v1.3.0: 연결 테스트, 날짜별 목록, 단건, 일괄 등록·취소 (200건씩, dryRun)
//	✓ models.go     네이버 예약(Reservation) → API 항목(ImportItem) 변환·검증
//	✓ store.go      SQLite 아웃박스: 긁어 온 예약 저장, 바뀐 것만 전송, 서버가 처리한 것만 "전송 완료"
//	… host.go       Native Messaging 요청 처리 (PageVault core/nativemsg.go 구조를 따른다)
package core
