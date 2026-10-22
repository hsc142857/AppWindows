package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"
	"unicode/utf8"

	"appwindows/shared/apiclient"
)

// Motospec Visit Import API (OpenAPI: /api/docs/visit-import/openapi, v1.3.0 — 기준본 docs/visit-import-openapi.json).
// 경로는 하나뿐이다: GET(쿼리 없음)=연결 테스트, GET ?date=지정일 목록, GET ?externalBookingId=단건,
// POST=일괄 등록·취소 (dryRun이면 DB에 쓰지 않고 예상 결과만).
// 인증키를 발급한 업체(partnerId·partnerName)는 GET 응답에만 온다 — POST·dryRun 응답에는 없다.
const (
	ImportPath     = "/api/visit/import-external"
	MaxImportItems = 200 // POST 한 번에 보낼 수 있는 최대 항목 수
	ActionUpsert   = "upsert"
	ActionCancel   = "cancel"
)

// ImportItem은 POST 항목이다. externalBookingId(네이버 예약번호)가 파트너 범위의 멱등 키다.
type ImportItem struct {
	ExternalBookingID string `json:"externalBookingId"`     // 1~80자
	ScheduledAt       string `json:"scheduledAt,omitempty"` // ISO-8601, upsert 필수 (예: 2026-10-20T14:00:00+09:00)
	Name              string `json:"name,omitempty"`        // ≤40
	Phone             string `json:"phone,omitempty"`       // 01…, upsert 필수, ≤20
	PlateNo           string `json:"plateNo,omitempty"`     // ≤20, 없으면 서버가 NAVERIMPORT 자리표시
	Brand             string `json:"brand,omitempty"`       // ≤40
	Model             string `json:"model,omitempty"`       // ≤40
	Year              *int   `json:"year,omitempty"`        // 1980~2100
	Mileage           *int   `json:"mileage,omitempty"`     // 0~9,999,999
	RequestNote       string `json:"requestNote,omitempty"` // ≤1000
	Action            string `json:"action"`                // upsert | cancel
	StatusHint        string `json:"statusHint,omitempty"`  // ≤40, 네이버 원래 상태 등
}

type ImportError struct {
	ExternalBookingID string `json:"externalBookingId"`
	Error             string `json:"error"`
}

// ImportItemResult는 건별 처리 결과다 (v1.2.0).
type ImportItemResult struct {
	ExternalBookingID string  `json:"externalBookingId"`
	Result            string  `json:"result"`      // created · updated · skipped · cancelled · error
	Error             string  `json:"error"`       // result=error일 때만
	ScheduledAt       *string `json:"scheduledAt"` // 서버에 저장된 일시 (UTC)
	Status            string  `json:"status"`      // 성공 시 방문 상태 (가능하면)
}

type ImportResult struct {
	DryRun    bool               `json:"dryRun"`
	Created   int                `json:"created"`
	Updated   int                `json:"updated"`
	Skipped   int                `json:"skipped"`
	Cancelled int                `json:"cancelled"`
	Results   []ImportItemResult `json:"results"` // 건별 결과 (로컬 검증 실패도 result=error로 넣는다)
	Errors    []ImportError      `json:"errors"`  // results 중 error만 (서버 하위 호환 필드)
}

func (r *ImportResult) add(o ImportResult) {
	r.Created += o.Created
	r.Updated += o.Updated
	r.Skipped += o.Skipped
	r.Cancelled += o.Cancelled
	r.Results = append(r.Results, o.Results...)
	r.Errors = append(r.Errors, o.Errors...)
}

func (r *ImportResult) addLocalError(id, msg string) {
	r.Results = append(r.Results, ImportItemResult{ExternalBookingID: id, Result: "error", Error: msg})
	r.Errors = append(r.Errors, ImportError{ExternalBookingID: id, Error: msg})
}

type PingResult struct {
	OK           bool     `json:"ok"`
	PartnerID    string   `json:"partnerId"`
	PartnerName  string   `json:"partnerName"` // v1.3.0~
	Scopes       []string `json:"scopes"`
	AllowedPaths []string `json:"allowedPaths"`
}

type ListVisitItem struct {
	ExternalBookingID string  `json:"externalBookingId"`
	Status            string  `json:"status"` // reserved · cancelled · waiting · done 등
	ScheduledAt       *string `json:"scheduledAt"`
	Name              string  `json:"name"`
	Phone             string  `json:"phone"`
	PlateNo           string  `json:"plateNo"`
	Brand             string  `json:"brand"`
	Model             string  `json:"model"`
	Year              *int    `json:"year"`
	Mileage           int     `json:"mileage"`
	RequestNote       string  `json:"requestNote"`
	Source            string  `json:"source"`
}

type ListResult struct {
	PartnerID   string          `json:"partnerId"`
	PartnerName string          `json:"partnerName"`
	Date        string          `json:"date"`
	Visits      []ListVisitItem `json:"visits"`
}

// OneResult는 단건 조회 응답이다.
type OneResult struct {
	PartnerID   string        `json:"partnerId"`
	PartnerName string        `json:"partnerName"`
	Visit       ListVisitItem `json:"visit"`
}

// Ping은 인증키·파트너 연결을 확인한다.
func Ping(ctx context.Context, c apiclient.Config) (PingResult, error) {
	cl, err := newClient(c)
	if err != nil {
		return PingResult{}, err
	}
	var out PingResult
	if err := cl.DoJSON(ctx, http.MethodGet, ImportPath, nil, &out); err != nil {
		return out, err
	}
	if !out.OK {
		return out, fmt.Errorf("서버가 연결 실패로 응답했습니다")
	}
	return out, nil
}

// Seoul은 서버가 날짜를 해석하는 시간대(서울)다. tzdata가 없는 환경이면 +09:00 고정으로 대신한다.
var Seoul = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Seoul"); err == nil {
		return loc
	}
	return time.FixedZone("KST", 9*60*60)
}()

// ValidateDate는 목록 조회일(YYYY-MM-DD, 서울 달력 하루)을 검사한다.
func ValidateDate(date string) error {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return fmt.Errorf("날짜 형식은 YYYY-MM-DD 입니다: %q", date)
	}
	return nil
}

// ListDay는 서버에 저장된 지정일(서울) 예약을 돌려준다 (취소 포함, 최대 500건). 스크랩 결과와 비교할 때 쓴다.
func ListDay(ctx context.Context, c apiclient.Config, date string) (ListResult, error) {
	if err := ValidateDate(date); err != nil {
		return ListResult{}, err
	}
	cl, err := newClient(c)
	if err != nil {
		return ListResult{}, err
	}
	var out struct {
		ListResult
		Visits *[]ListVisitItem `json:"visits"`
		OK     *bool            `json:"ok"`
	}
	q := url.Values{"date": {date}}
	if err := cl.DoJSON(ctx, http.MethodGet, ImportPath+"?"+q.Encode(), nil, &out); err != nil {
		return ListResult{}, err
	}
	if out.Visits == nil {
		// 목록 조회를 지원하지 않는 서버(v1.2.0 이전)는 쿼리를 무시하고 연결 테스트 응답을 준다.
		if out.OK != nil {
			return ListResult{}, fmt.Errorf("서버가 날짜별 목록 조회를 지원하지 않습니다 (연결 테스트 응답이 왔습니다 — 서버 버전 확인 필요)")
		}
		return ListResult{}, fmt.Errorf("서버 응답에 visits가 없습니다")
	}
	res := out.ListResult
	res.Visits = *out.Visits
	return res, nil
}

// GetVisit은 예약번호로 서버에 저장된 예약 한 건을 찾는다. 없으면 found=false.
func GetVisit(ctx context.Context, c apiclient.Config, externalBookingID string) (res OneResult, found bool, err error) {
	if externalBookingID == "" || utf8.RuneCountInString(externalBookingID) > 80 {
		return res, false, fmt.Errorf("예약번호는 1~80자입니다")
	}
	cl, err := newClient(c)
	if err != nil {
		return res, false, err
	}
	var out struct {
		PartnerID   string         `json:"partnerId"`
		PartnerName string         `json:"partnerName"`
		Visit       *ListVisitItem `json:"visit"`
	}
	q := url.Values{"externalBookingId": {externalBookingID}}
	err = cl.DoJSON(ctx, http.MethodGet, ImportPath+"?"+q.Encode(), nil, &out)
	var apiErr *apiclient.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return res, false, nil
	}
	if err != nil {
		return res, false, err
	}
	if out.Visit == nil {
		return res, false, fmt.Errorf("서버 응답에 visit이 없습니다 (서버 버전 확인 필요)")
	}
	return OneResult{PartnerID: out.PartnerID, PartnerName: out.PartnerName, Visit: *out.Visit}, true, nil
}

// Import는 항목을 200건씩 나눠 보낸다. 형식이 틀린 항목은 보내지 않고 result=error로 넣는다
// (서버가 요청 전체를 400으로 거절하지 않도록 미리 거른다).
// dryRun이면 서버가 DB에 쓰지 않고 건별 예상 결과만 돌려준다.
// 전송 중 오류가 나면 그때까지의 결과와 함께 오류를 돌려준다.
func Import(ctx context.Context, c apiclient.Config, items []ImportItem, dryRun bool) (res ImportResult, err error) {
	res.DryRun = dryRun
	valid := make([]ImportItem, 0, len(items))
	for _, it := range items {
		if err := it.Validate(); err != nil {
			res.addLocalError(it.ExternalBookingID, err.Error())
			continue
		}
		valid = append(valid, it)
	}
	if len(valid) == 0 {
		return res, nil
	}
	cl, err := newClient(c)
	if err != nil {
		return res, err
	}
	// 건별 결과는 입력 순서로 돌려준다 (로컬 검증 오류와 서버 결과가 섞여도).
	defer func() {
		order := make(map[string]int, len(items))
		for i, it := range items {
			if _, dup := order[it.ExternalBookingID]; !dup {
				order[it.ExternalBookingID] = i
			}
		}
		sort.SliceStable(res.Results, func(i, j int) bool {
			return order[res.Results[i].ExternalBookingID] < order[res.Results[j].ExternalBookingID]
		})
	}()
	for start := 0; start < len(valid); start += MaxImportItems {
		end := min(start+MaxImportItems, len(valid))
		var out ImportResult
		body := map[string]any{"items": valid[start:end], "dryRun": dryRun}
		if err := cl.DoJSON(ctx, http.MethodPost, ImportPath, body, &out); err != nil {
			return res, fmt.Errorf("예약 업로드 실패 (%d~%d번째): %w", start+1, end, err)
		}
		res.add(out)
	}
	return res, nil
}
