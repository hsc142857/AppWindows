package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"appwindows/shared/apiclient"
)

const testKey = "msvi_testkey"

// fakeVisitAPI는 OpenAPI v1.3.0 명세대로 동작하는 테스트 서버다.
// 실제 서버처럼 항목 하나라도 형식이 틀리면 요청 전체를 400으로 거절한다.
// 업체 정보는 GET 응답에만 넣는다.
type fakeVisitAPI struct {
	posts       int
	visits      map[string]ImportItem
	partnerID   string
	partnerName string
	fail        bool   // true면 POST에 503 (서버 장애 흉내)
	onPost      func() // POST를 처리한 직후 실행 (보내는 사이의 변경 흉내)
}

func newFakeVisitAPI(t *testing.T) (*fakeVisitAPI, apiclient.Config) {
	f := &fakeVisitAPI{visits: map[string]ImportItem{}, partnerID: "p1", partnerName: "테스트모터스"}
	writeErr := func(w http.ResponseWriter, code int, msg string) {
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ImportPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		switch r.Method {
		case http.MethodGet:
			date, id := r.URL.Query().Get("date"), r.URL.Query().Get("externalBookingId")
			switch {
			case date != "" && id != "":
				writeErr(w, http.StatusBadRequest, "validation")
			case id != "":
				it, ok := f.visits[id]
				if !ok {
					writeErr(w, http.StatusNotFound, "not_found")
					return
				}
				json.NewEncoder(w).Encode(OneResult{PartnerID: f.partnerID, PartnerName: f.partnerName,
					Visit: ListVisitItem{ExternalBookingID: id, Status: "reserved", Name: it.Name}})
			case date != "":
				if ValidateDate(date) != nil {
					writeErr(w, http.StatusBadRequest, "invalid_date")
					return
				}
				res := ListResult{PartnerID: f.partnerID, PartnerName: f.partnerName, Date: date, Visits: []ListVisitItem{}}
				for _, it := range f.visits {
					if strings.HasPrefix(it.ScheduledAt, date) {
						res.Visits = append(res.Visits, ListVisitItem{ExternalBookingID: it.ExternalBookingID, Status: "reserved", Name: it.Name})
					}
				}
				json.NewEncoder(w).Encode(res)
			default:
				json.NewEncoder(w).Encode(PingResult{OK: true, PartnerID: f.partnerID, PartnerName: f.partnerName, Scopes: []string{"visit.import"}})
			}
		case http.MethodPost:
			var req struct {
				Items  []ImportItem `json:"items"`
				DryRun bool         `json:"dryRun"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			f.posts++
			if f.fail {
				writeErr(w, http.StatusServiceUnavailable, "unavailable")
				return
			}
			if f.onPost != nil {
				defer f.onPost()
			}
			if len(req.Items) > MaxImportItems {
				writeErr(w, http.StatusBadRequest, "validation")
				return
			}
			for _, it := range req.Items {
				if it.Validate() != nil {
					writeErr(w, http.StatusBadRequest, "validation")
					return
				}
			}
			res := ImportResult{DryRun: req.DryRun}
			for _, it := range req.Items {
				prev, exists := f.visits[it.ExternalBookingID]
				result := ""
				switch {
				case it.Action == ActionCancel && !exists:
					result = "error"
				case it.Action == ActionCancel:
					result = "cancelled"
					res.Cancelled++
					if !req.DryRun {
						delete(f.visits, it.ExternalBookingID)
					}
				case exists && prev == it:
					result = "skipped"
					res.Skipped++
				default:
					if exists {
						result = "updated"
						res.Updated++
					} else {
						result = "created"
						res.Created++
					}
					if !req.DryRun {
						f.visits[it.ExternalBookingID] = it
					}
				}
				ir := ImportItemResult{ExternalBookingID: it.ExternalBookingID, Result: result}
				if result == "error" {
					ir.Error = "not_found"
					res.Errors = append(res.Errors, ImportError{ExternalBookingID: it.ExternalBookingID, Error: "not_found"})
				}
				res.Results = append(res.Results, ir)
			}
			json.NewEncoder(w).Encode(res)
		}
	}))
	t.Cleanup(srv.Close)
	return f, apiclient.Config{BaseURL: srv.URL, Token: testKey}
}

func reservation(id string) Reservation {
	return Reservation{BookingID: id, Status: "확정", StartAt: "2026-10-20T14:00:00+09:00", ItemName: "점검",
		Customer: "홍길동", Phone: "010-1234-5678", Request: "엔진 소음", Year: 0, Mileage: -1}
}

func TestPing(t *testing.T) {
	_, cfg := newFakeVisitAPI(t)
	p, err := Ping(context.Background(), cfg)
	if err != nil || p.PartnerID != "p1" || p.PartnerName != "테스트모터스" {
		t.Fatalf("ping = %+v, %v", p, err)
	}
	cfg.Token = "msvi_wrong"
	if _, err := Ping(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("wrong key should fail with 401: %v", err)
	}
}

func TestImportBatchesAndResults(t *testing.T) {
	f, cfg := newFakeVisitAPI(t)
	items := make([]ImportItem, 0, 450)
	for i := 0; i < 450; i++ {
		items = append(items, reservation(fmt.Sprintf("B%04d", i)).ToImportItem())
	}
	res, err := Import(context.Background(), cfg, items, false)
	if err != nil || res.Created != 450 || len(res.Errors) != 0 {
		t.Fatalf("import = %+v, %v", res, err)
	}
	if f.posts != 3 {
		t.Fatalf("posts = %d, want 3 (200+200+50)", f.posts)
	}
	// 같은 내용 재전송 → skipped, 바뀐 것 → updated, 취소 → cancelled
	changed := reservation("B0001")
	changed.Request = "체인 교체"
	cancelled := reservation("B0002")
	cancelled.Status = "예약자 취소"
	second := []ImportItem{reservation("B0000").ToImportItem(), changed.ToImportItem(), cancelled.ToImportItem()}
	// dryRun은 예상 결과만 돌려주고 서버 데이터를 바꾸지 않는다.
	dry, err := Import(context.Background(), cfg, second, true)
	if err != nil || !dry.DryRun || dry.Skipped != 1 || dry.Updated != 1 || dry.Cancelled != 1 || len(f.visits) != 450 {
		t.Fatalf("dry run = %+v, %v, visits=%d", dry, err, len(f.visits))
	}
	if len(dry.Results) != 3 || dry.Results[1].Result != "updated" || dry.Results[2].Result != "cancelled" {
		t.Fatalf("dry run results = %+v", dry.Results)
	}
	res, err = Import(context.Background(), cfg, second, false)
	if err != nil || res.Skipped != 1 || res.Updated != 1 || res.Cancelled != 1 || len(f.visits) != 449 {
		t.Fatalf("second import = %+v, %v", res, err)
	}
}

func TestImportSkipsInvalidItemsLocally(t *testing.T) {
	f, cfg := newFakeVisitAPI(t)
	bad := reservation("B1")
	bad.Phone = "02-123-4567" // 휴대폰 아님
	noTime := reservation("B2")
	noTime.StartAt = ""
	res, err := Import(context.Background(), cfg, []ImportItem{
		bad.ToImportItem(), noTime.ToImportItem(), reservation("B3").ToImportItem(),
	}, false)
	// 틀린 항목 때문에 요청 전체가 400이 되지 않아야 한다.
	if err != nil || res.Created != 1 || len(res.Errors) != 2 || f.posts != 1 {
		t.Fatalf("import = %+v, %v, posts=%d", res, err, f.posts)
	}
	// 로컬 검증 실패도 건별 결과(result=error)에 입력 순서대로 들어간다.
	if len(res.Results) != 3 || res.Results[0].ExternalBookingID != "B1" || res.Results[1].ExternalBookingID != "B2" ||
		res.Results[2].Result != "created" {
		t.Fatalf("results = %+v", res.Results)
	}
	if strings.Contains(res.Errors[0].Error, "1234567") {
		t.Fatalf("phone must be masked in errors: %q", res.Errors[0].Error)
	}
	// 전부 틀리면 요청을 보내지 않는다.
	res, err = Import(context.Background(), cfg, []ImportItem{bad.ToImportItem()}, false)
	if err != nil || len(res.Errors) != 1 || f.posts != 1 {
		t.Fatalf("all invalid = %+v, %v, posts=%d", res, err, f.posts)
	}
}

func TestListDayAndGet(t *testing.T) {
	_, cfg := newFakeVisitAPI(t)
	ctx := context.Background()
	Import(ctx, cfg, []ImportItem{reservation("B1").ToImportItem()}, false)
	res, err := ListDay(ctx, cfg, "2026-10-20")
	if err != nil || res.Date != "2026-10-20" || len(res.Visits) != 1 || res.Visits[0].ExternalBookingID != "B1" || res.PartnerName != "테스트모터스" {
		t.Fatalf("list = %+v, %v", res, err)
	}
	if res, err := ListDay(ctx, cfg, "2026-10-21"); err != nil || len(res.Visits) != 0 {
		t.Fatalf("empty day = %+v, %v", res, err)
	}
	for _, d := range []string{"2026-13-01", "20261020", ""} {
		if _, err := ListDay(ctx, cfg, d); err == nil {
			t.Errorf("date %q should fail", d)
		}
	}
	v, found, err := GetVisit(ctx, cfg, "B1")
	if err != nil || !found || v.Visit.Name != "홍길동" || v.PartnerName != "테스트모터스" {
		t.Fatalf("get = %+v, %v, %v", v, found, err)
	}
	if _, found, err := GetVisit(ctx, cfg, "NOPE"); err != nil || found {
		t.Fatalf("missing booking should be found=false without error: %v, %v", found, err)
	}
	if _, _, err := GetVisit(ctx, cfg, ""); err == nil {
		t.Fatal("empty booking id should fail")
	}

	// 목록을 지원하지 않는 서버(쿼리를 무시하고 핑 응답)는 "예약 없음"이 아니라 오류로 보고한다.
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(PingResult{OK: true, PartnerID: "p1"})
	}))
	defer old.Close()
	oldCfg := apiclient.Config{BaseURL: old.URL, Token: testKey}
	if _, err := ListDay(ctx, oldCfg, "2026-10-20"); err == nil || !strings.Contains(err.Error(), "지원하지 않습니다") {
		t.Fatalf("ping-only server should be reported: %v", err)
	}
	if _, _, err := GetVisit(ctx, oldCfg, "B1"); err == nil {
		t.Fatal("ping-only server should be reported for single lookup")
	}
}

func TestToImportItem(t *testing.T) {
	r := reservation("1234567890")
	r.Phone = "+82 10-1234-5678"
	r.Customer = strings.Repeat("가", 50)
	r.PlateNo = "서울12가3456"
	r.Year = 2021
	r.Mileage = 12000
	it := r.ToImportItem()
	if it.Action != ActionUpsert || it.Phone != "01012345678" || it.ScheduledAt != r.StartAt {
		t.Fatalf("item = %+v", it)
	}
	if len([]rune(it.Name)) != 40 || it.Year == nil || *it.Year != 2021 || it.Mileage == nil || *it.Mileage != 12000 {
		t.Fatalf("item = %+v", it)
	}
	if !strings.HasPrefix(it.RequestNote, "서비스:점검\n") {
		t.Fatalf("requestNote = %q", it.RequestNote)
	}
	if err := it.Validate(); err != nil {
		t.Fatal(err)
	}
	// 연식·주행거리를 모르면 보내지 않는다.
	unknown := reservation("x").ToImportItem()
	if unknown.Year != nil || unknown.Mileage != nil {
		t.Fatalf("unknown year/mileage must be omitted: %+v", unknown)
	}
	b, _ := json.Marshal(unknown)
	if strings.Contains(string(b), "year") || strings.Contains(string(b), "mileage") {
		t.Fatalf("json = %s", b)
	}
	// 취소는 예약번호와 action만
	c := reservation("y")
	c.Status = "업체 취소"
	ci := c.ToImportItem()
	if ci.Action != ActionCancel || ci.Phone != "" || ci.Name != "" || ci.Validate() != nil {
		t.Fatalf("cancel item = %+v", ci)
	}
}

func TestAPIConfig(t *testing.T) {
	t.Setenv("NAVERBOOKINGSCRAPER_HOME", t.TempDir())
	if _, err := LoadAPIConfig(); err != ErrNoAPIKey {
		t.Fatalf("missing config should be ErrNoAPIKey, got %v", err)
	}
	if _, err := NewAPIConfig("", "wrong_prefix"); err == nil {
		t.Fatal("key without msvi_ prefix should fail")
	}
	c, err := NewAPIConfig("", testKey)
	if err != nil || c.BaseURL != DefaultBaseURL {
		t.Fatalf("default base URL: %+v, %v", c, err)
	}
	if err := SaveAPIConfig(c); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAPIConfig()
	if err != nil || got.Token != testKey {
		t.Fatalf("load = %+v, %v", got, err)
	}
}

func TestCheckPartner(t *testing.T) {
	t.Setenv("NAVERBOOKINGSCRAPER_HOME", t.TempDir())
	f, cfg := newFakeVisitAPI(t)
	ctx := context.Background()
	if _, ok, _ := LoadPartner(); ok {
		t.Fatal("no partner should be remembered yet")
	}
	p, change, err := CheckPartner(ctx, cfg)
	if err != nil || p.ID != "p1" || p.Label() != "테스트모터스" || change.Changed {
		t.Fatalf("first check = %+v, %+v, %v", p, change, err)
	}
	saved, ok, err := LoadPartner()
	if err != nil || !ok || saved.Name != "테스트모터스" || saved.CheckedAt == "" {
		t.Fatalf("saved = %+v, %v, %v", saved, ok, err)
	}
	// 같은 업체: 바뀜 아님 (이름만 바뀌어도 업체는 같다)
	f.partnerName = "테스트모터스 본점"
	if p, change, _ := CheckPartner(ctx, cfg); change.Changed || p.Name != "테스트모터스 본점" {
		t.Fatalf("rename = %+v, %+v", p, change)
	}
	// 다른 업체의 키로 바뀜 → 경고
	f.partnerID, f.partnerName = "p2", "다른모터스"
	p, change, err = CheckPartner(ctx, cfg)
	if err != nil || !change.Changed || change.Previous.ID != "p1" || p.ID != "p2" {
		t.Fatalf("partner change = %+v, %+v, %v", p, change, err)
	}
	// 키 오류면 업체 정보를 바꾸지 않는다.
	bad := cfg
	bad.Token = "msvi_wrong"
	if _, _, err := CheckPartner(ctx, bad); err == nil {
		t.Fatal("wrong key should fail")
	}
	if saved, _, _ := LoadPartner(); saved.ID != "p2" {
		t.Fatalf("failed check must not change saved partner: %+v", saved)
	}
	// 인증키를 지우면 업체 정보도 지운다.
	SaveAPIConfig(cfg)
	if err := ClearAPIConfig(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := LoadPartner(); ok {
		t.Fatal("partner should be cleared with the key")
	}
}

func TestPartnerLabel(t *testing.T) {
	cases := map[Partner]string{{ID: "p1", Name: "가게"}: "가게", {ID: "p1"}: "업체 p1", {}: "업체 미확인"}
	for p, want := range cases {
		if got := p.Label(); got != want {
			t.Errorf("Label(%+v) = %q, want %q", p, got, want)
		}
	}
}

func TestSetupAPI(t *testing.T) {
	t.Setenv("NAVERBOOKINGSCRAPER_HOME", t.TempDir())
	_, cfg := newFakeVisitAPI(t)
	ctx := context.Background()
	if _, _, _, err := SetupAPI(ctx, cfg.BaseURL, ""); err == nil {
		t.Fatal("first setup without a key should fail")
	}
	if _, _, _, err := SetupAPI(ctx, cfg.BaseURL, "msvi_wrong"); err == nil || !strings.Contains(err.Error(), "맞지 않습니다") {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := LoadAPIConfig(); err != ErrNoAPIKey {
		t.Fatal("wrong key must not be saved")
	}
	c, p, _, err := SetupAPI(ctx, cfg.BaseURL, testKey)
	if err != nil || c.Token != testKey || p.Name != "테스트모터스" {
		t.Fatalf("setup = %+v, %+v, %v", c, p, err)
	}
	// 키를 비우면 저장된 키를 유지한다 (주소만 바꿀 때).
	c, _, _, err = SetupAPI(ctx, cfg.BaseURL+"/", "")
	if err != nil || c.Token != testKey {
		t.Fatalf("keep key = %+v, %v", c, err)
	}
	if _, _, _, err := SetupAPI(ctx, "http://127.0.0.1:1", testKey); err == nil || !strings.Contains(err.Error(), "연결 실패") {
		t.Fatalf("unreachable server: %v", err)
	}
}
