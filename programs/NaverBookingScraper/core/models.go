package core

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Reservation은 네이버 예약 파트너센터에서 읽은 예약 한 건이다 (extension/parse.js가 채운다).
// 차량 정보(차량번호·브랜드·모델·연식·주행거리)는 예약 폼의 추가 질문에서 읽을 수 있을 때만 채운다.
// 필드는 실제 파트너센터 화면을 확인한 뒤 확정한다.
type Reservation struct {
	BookingID   string `json:"bookingID"`   // 네이버 예약번호 → externalBookingId
	Status      string `json:"status"`      // 네이버 원래 상태 (확정, 대기, 취소, 이용완료 …)
	StartAt     string `json:"startAt"`     // 이용 일시 (ISO-8601, +09:00)
	ItemName    string `json:"itemName"`    // 상품(서비스) 이름
	Customer    string `json:"customer"`    // 예약자 이름
	Phone       string `json:"phone"`       // 예약자 전화
	Request     string `json:"request"`     // 요청사항
	PlateNo     string `json:"plateNo"`     // 추가 질문: 차량번호
	Brand       string `json:"brand"`       // 추가 질문: 브랜드
	Model       string `json:"model"`       // 추가 질문: 모델
	Year        int    `json:"year"`        // 추가 질문: 연식 (모르면 0)
	Mileage     int    `json:"mileage"`     // 추가 질문: 주행거리 km (모르면 -1)
	CapturedAt  string `json:"capturedAt"`  // 수집 시각 (UTC)
	BusinessURL string `json:"businessURL"` // 수집한 예약 목록 페이지
}

// IsCancelled는 네이버 상태가 취소 계열인지 판단한다 (예약자 취소·업체 취소·노쇼 취소 등).
func (r Reservation) IsCancelled() bool {
	return strings.Contains(r.Status, "취소")
}

// ToImportItem은 예약을 API 항목으로 바꾼다. 취소면 cancel, 아니면 upsert.
// 길이 제한을 넘는 텍스트는 잘라 낸다 (검증은 ImportItem.Validate).
func (r Reservation) ToImportItem() ImportItem {
	it := ImportItem{
		ExternalBookingID: strings.TrimSpace(r.BookingID),
		StatusHint:        truncate(r.Status, 40),
	}
	if r.IsCancelled() {
		it.Action = ActionCancel
		return it
	}
	it.Action = ActionUpsert
	it.ScheduledAt = strings.TrimSpace(r.StartAt)
	it.Name = truncate(r.Customer, 40)
	it.Phone = NormalizePhone(r.Phone)
	it.PlateNo = truncate(r.PlateNo, 20)
	it.Brand = truncate(r.Brand, 40)
	it.Model = truncate(r.Model, 40)
	if r.Year >= 1980 && r.Year <= 2100 {
		y := r.Year
		it.Year = &y
	}
	if r.Mileage >= 0 && r.Mileage <= 9_999_999 {
		m := r.Mileage
		it.Mileage = &m
	}
	note := strings.TrimSpace(r.Request)
	if item := strings.TrimSpace(r.ItemName); item != "" {
		note = strings.TrimSpace("서비스:" + item + "\n" + note)
	}
	it.RequestNote = truncate(note, 1000)
	return it
}

// NormalizePhone은 숫자만 남기고 +82 국가번호를 0으로 바꾼다 (예: +82 10-1234-5678 → 01012345678).
func NormalizePhone(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if strings.HasPrefix(d, "82") && len(d) >= 11 {
		d = "0" + d[2:]
	}
	return d
}

func isMobile(phone string) bool {
	if !strings.HasPrefix(phone, "01") || len(phone) < 10 || len(phone) > 11 {
		return false
	}
	return true
}

// Validate는 서버 스키마(ImportItem)에 맞는지 검사한다.
func (it ImportItem) Validate() error {
	id := it.ExternalBookingID
	if id == "" || utf8.RuneCountInString(id) > 80 {
		return errors.New("예약번호(externalBookingId)가 비었거나 80자를 넘습니다")
	}
	switch it.Action {
	case ActionCancel:
		return nil
	case ActionUpsert:
	default:
		return fmt.Errorf("action은 upsert 또는 cancel이어야 합니다: %q", it.Action)
	}
	if it.ScheduledAt == "" {
		return errors.New("이용 일시(scheduledAt)가 없습니다")
	}
	if _, err := time.Parse(time.RFC3339, it.ScheduledAt); err != nil {
		return fmt.Errorf("이용 일시 형식 오류 (ISO-8601 예: 2026-10-20T14:00:00+09:00): %q", it.ScheduledAt)
	}
	if !isMobile(it.Phone) {
		return fmt.Errorf("휴대폰 번호(01…)가 아닙니다: %q", maskPhone(it.Phone))
	}
	for _, f := range []struct {
		name, v string
		max     int
	}{{"name", it.Name, 40}, {"plateNo", it.PlateNo, 20}, {"brand", it.Brand, 40}, {"model", it.Model, 40},
		{"requestNote", it.RequestNote, 1000}, {"statusHint", it.StatusHint, 40}} {
		if utf8.RuneCountInString(f.v) > f.max {
			return fmt.Errorf("%s가 %d자를 넘습니다", f.name, f.max)
		}
	}
	return nil
}

// maskPhone은 로그·오류에 쓸 때 가운데를 가린다 (01012345678 → 010****5678).
func maskPhone(p string) string {
	if len(p) < 7 {
		return strings.Repeat("*", len(p))
	}
	return p[:3] + strings.Repeat("*", len(p)-7) + p[len(p)-4:]
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
