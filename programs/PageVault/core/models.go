package core

import (
	"fmt"
	"time"
)

const (
	StatusOK            = "ok"
	StatusLoginRequired = "login_required"
	StatusError         = "error"

	DefaultIntervalSeconds  = 3600
	MinIntervalSeconds      = 30
	DefaultLoadDelaySeconds = 3
	MaxLoadDelaySeconds     = 120
)

type Target struct {
	ID        int64  `json:"id"`
	URL       string `json:"url"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"createdAt"`
}

// Capture는 확장이 한 페이지에서 추출해 보내는 값이다.
type Capture struct {
	TargetID     int64  `json:"targetID"`
	RequestedURL string `json:"requestedURL"`
	FinalURL     string `json:"finalURL"`
	Title        string `json:"title"`
	Text         string `json:"text"`
	HTML         string `json:"html"`
	Error        string `json:"error"`
}

type Snapshot struct {
	ID          int64  `json:"id"`
	UID         string `json:"uid"`
	TargetID    *int64 `json:"targetID"`
	TargetURL   string `json:"targetURL"`
	TargetName  string `json:"targetName"`
	FinalURL    string `json:"finalURL"`
	Title       string `json:"title"`
	Text        string `json:"text,omitempty"`
	HTML        string `json:"html,omitempty"`
	ContentHash string `json:"contentHash"`
	Status      string `json:"status"`
	Error       string `json:"error"`
	CapturedAt  string `json:"capturedAt"`
	LastSeenAt  string `json:"lastSeenAt"`
	SyncedAt    string `json:"syncedAt"`
	SyncError   string `json:"syncError"` // 서버가 거부한 사유 (있으면 재전송하지 않음)
}

type Settings struct {
	IntervalSeconds  int `json:"intervalSeconds"`
	LoadDelaySeconds int `json:"loadDelaySeconds"`
}

// TargetState는 대상별 마지막 스냅샷 상태 (트레이의 로그인 만료 알림에 쓴다).
type TargetState struct {
	Target
	LastStatus     string `json:"lastStatus"`
	LastCapturedAt string `json:"lastCapturedAt"`
	LastSeenAt     string `json:"lastSeenAt"`
}

type Status struct {
	Home          string        `json:"home"`
	Targets       int           `json:"targets"`
	EnabledCount  int           `json:"enabled"`
	Snapshots     int           `json:"snapshots"`
	Unsynced      int           `json:"unsynced"`
	Rejected      int           `json:"rejected"` // 서버가 거부해 업로드에서 빠진 건수
	LastCapture   string        `json:"lastCapture"`
	Settings      Settings      `json:"settings"`
	API           string        `json:"api"`
	LoginRequired []TargetState `json:"loginRequired"`
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
