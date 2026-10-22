// 예약 목록 페이지에서 예약 배열을 추출한다 (골격).
// 선택자·필드는 실제 파트너센터 페이지를 확인한 뒤 확정한다.
// 페이지가 내부 JSON API로 목록을 받아 오면 DOM 대신 그 응답을 읽는 방식을 우선 검토한다.
(() => {
  const reservations = [];
  // TODO: 목록 행을 순회하며 core.Reservation 필드(bookingID, status, startAt, itemName, customer, phone, request,
  //       추가 질문의 plateNo·brand·model·year·mileage)를 채운다.
  return { href: location.href, title: document.title, reservations };
})();
