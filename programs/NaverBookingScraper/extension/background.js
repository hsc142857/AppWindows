// NaverBookingScraper background service worker (골격)
// 흐름: 알람 → 호스트(config) → 업체별 예약 목록 페이지 열기 → parse.js로 예약 추출 → 호스트(save) → 호스트(upload)
// 수집·라운드 관리 방식은 PageVault extension/background.js와 같게 만든다.

const HOST = "com.appwindows.naverbookingscraper";
const ALARM = "naverbookingscraper-collect";

chrome.runtime.onInstalled.addListener(() => {
  // TODO: 호스트에 config를 물어 주기에 맞춰 알람 설정
});

chrome.alarms.onAlarm.addListener((a) => {
  if (a.name === ALARM) collectRound();
});

async function collectRound() {
  // TODO:
  // 1. connectNative(HOST) → config {intervalSeconds, businesses:[{id, url, name}]}
  // 2. 최소화 창에서 업체별 예약 목록 URL 열기 (로그인 만료 시 login_required 보고)
  // 3. chrome.scripting.executeScript({ files: ["parse.js"] }) 로 예약 배열 추출
  // 4. save {businessURL, reservations:[…]} → 호스트가 바뀐 것만 Visit Import API로 업로드 → 창 닫기
}
