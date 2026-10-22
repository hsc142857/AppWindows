package core

import (
	"fmt"
	"strings"
	"time"
)

// 수집(capture) 한 번의 결과 상태.
const (
	StatusOK            = "ok"
	StatusEmpty         = "empty" // 페이지는 열렸지만 표·행을 찾지 못함 (선택자 확인)
	StatusLoginRequired = "login_required"
	StatusError         = "error"

	DefaultIntervalSeconds  = 600
	MinIntervalSeconds      = 30
	DefaultLoadDelaySeconds = 3
	MaxLoadDelaySeconds     = 120

	maxCellRunes = 4000 // 셀 하나의 최대 길이 (넘으면 자른다)
	MaxRows      = 5000 // 한 번에 받는 최대 행 수
)

// Target은 활동 로그를 읽을 페이지다.
// Selector가 비어 있으면 확장이 행이 가장 많은 표를 고른다.
type Target struct {
	ID        int64  `json:"id"`
	URL       string `json:"url"`
	Name      string `json:"name"`
	Selector  string `json:"selector"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"createdAt"`
}

// Capture는 확장이 한 페이지에서 읽어 보내는 표다.
type Capture struct {
	TargetID     int64      `json:"targetID"`
	RequestedURL string     `json:"requestedURL"`
	FinalURL     string     `json:"finalURL"`
	Title        string     `json:"title"`
	Headers      []string   `json:"headers"`
	Rows         [][]string `json:"rows"`
	Error        string     `json:"error"`
}

// CaptureRecord는 수집 한 번의 기록이다: 언제 무엇이 몇 행 보였는지 (증빙용 이력).
type CaptureRecord struct {
	ID         int64  `json:"id"`
	TargetURL  string `json:"targetURL"`
	TargetName string `json:"targetName"`
	FinalURL   string `json:"finalURL"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Error      string `json:"error"`
	RowCount   int    `json:"rowCount"`
	NewRows    int    `json:"newRows"`
	CapturedAt string `json:"capturedAt"`
}

// Entry는 활동 로그 한 행이다. 같은 대상에서 같은 내용이 다시 보이면 새 행이 아니라 LastSeenAt만 바뀐다.
type Entry struct {
	ID          int64    `json:"id"`
	UID         string   `json:"uid"`
	TargetURL   string   `json:"targetURL"`
	TargetName  string   `json:"targetName"`
	Headers     []string `json:"headers"`
	Cells       []string `json:"cells"`
	RowHash     string   `json:"rowHash"`
	Occurrence  int      `json:"occurrence"` // 같은 내용의 행이 한 화면에 여러 번 있을 때 몇 번째인지 (1부터)
	FirstSeenAt string   `json:"firstSeenAt"`
	LastSeenAt  string   `json:"lastSeenAt"`
	SyncedAt    string   `json:"syncedAt"`
	SyncError   string   `json:"syncError"` // 서버가 거부한 사유 (있으면 재전송하지 않음)
}

// Text는 화면 표시용 한 줄이다.
func (e Entry) Text() string { return strings.Join(e.Cells, " | ") }

type Settings struct {
	IntervalSeconds  int `json:"intervalSeconds"`
	LoadDelaySeconds int `json:"loadDelaySeconds"`
}

// TargetState는 대상별 마지막 수집 상태다.
type TargetState struct {
	Target
	Entries        int    `json:"entries"`
	LastStatus     string `json:"lastStatus"`
	LastError      string `json:"lastError"`
	LastCapturedAt string `json:"lastCapturedAt"`
}

type Status struct {
	Home          string        `json:"home"`
	Targets       int           `json:"targets"`
	EnabledCount  int           `json:"enabled"`
	Entries       int           `json:"entries"`
	Unsynced      int           `json:"unsynced"`
	Rejected      int           `json:"rejected"`
	LastCapture   string        `json:"lastCapture"`
	Settings      Settings      `json:"settings"`
	NeedAttention []TargetState `json:"needAttention"` // 마지막 수집이 ok가 아닌 사용 중 대상
}

// 시각은 고정 폭 ISO8601 UTC (마이크로초) 문자열로 저장한다. 문자열 비교로 정렬된다.
const timeLayout = "2006-01-02T15:04:05.000000Z"

func FormatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func ParseTime(s string) (time.Time, error) { return time.Parse(timeLayout, s) }

func errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }

func ValidateInterval(sec int) error {
	if sec < MinIntervalSeconds {
		return errorf("수집 주기는 %d초 이상이어야 합니다 (입력: %d)", MinIntervalSeconds, sec)
	}
	return nil
}

func ValidateLoadDelay(sec int) error {
	if sec < 0 || sec > MaxLoadDelaySeconds {
		return errorf("로딩 대기는 0~%d초여야 합니다 (입력: %d)", MaxLoadDelaySeconds, sec)
	}
	return nil
}

// NormalizeCell은 공백을 한 칸으로 줄이고 앞뒤 공백을 지운다. 해시와 저장에 같은 값을 쓴다.
func NormalizeCell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxCellRunes {
		s = string(r[:maxCellRunes])
	}
	return s
}

// normalizeRow는 셀을 정리하고 끝의 빈 셀을 지운다. 모든 셀이 비면 nil.
func normalizeRow(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = NormalizeCell(c)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
