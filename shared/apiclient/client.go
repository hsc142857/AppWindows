// Package apiclient는 AppWindows 프로그램들이 데이터를 올릴 때 쓰는 공통 Web API 클라이언트다.
// 프로그램은 DB에 직접 연결하지 않고 이 클라이언트로 HTTP API를 호출한다.
// 계약(엔드포인트·요청·응답 형식)은 docs/web-api.md를 따른다.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	DefaultTimeout = 30 * time.Second
	// MaxBatchItems는 한 번에 올리는 최대 항목 수다 (서버 계약과 맞춘다).
	MaxBatchItems = 100
)

// Config는 접속 정보다. Token은 저장할 때 각 프로그램이 DPAPI 등으로 보호한다.
type Config struct {
	BaseURL string        `json:"baseURL"` // 예: https://api.example.com
	Token   string        `json:"token"`   // Authorization: Bearer <Token>
	Timeout time.Duration `json:"-"`
}

// Validate는 BaseURL 형식을 검사한다. https만 허용하되, 개발용으로 localhost와
// 사설망 IP(10.x, 172.16~31.x, 192.168.x — 예: Windows VM에서 Mac 개발 서버)는 http도 허용한다.
func (c Config) Validate() error {
	u, err := url.Parse(strings.TrimRight(c.BaseURL, "/"))
	if err != nil || u.Host == "" {
		return fmt.Errorf("API 주소가 올바르지 않습니다: %q", c.BaseURL)
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && isDevHost(u.Hostname()):
	default:
		return fmt.Errorf("API 주소는 https여야 합니다 (localhost·사설망 IP만 http 허용): %q", c.BaseURL)
	}
	if strings.TrimSpace(c.Token) == "" {
		return errors.New("API 토큰이 비어 있습니다")
	}
	return nil
}

// isDevHost는 http를 허용하는 개발용 주소인지 본다: localhost, 루프백, 사설망 IP.
func isDevHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

// MaskedToken은 화면 표시용으로 가린다. 길면 접두어(_까지)와 끝 4자만 남긴다 (msvi_****1TkO).
// 어떤 키가 저장되어 있는지 구분할 수 있을 만큼만 보여 준다.
func (c Config) MaskedToken() string {
	t := c.Token
	if len(t) < 12 {
		return "****"
	}
	prefix := ""
	if i := strings.IndexByte(t, '_'); i >= 0 && i < 8 {
		prefix = t[:i+1]
	}
	return prefix + "****" + t[len(t)-4:]
}

type Client struct {
	cfg        Config
	http       *http.Client
	sourceHost string
	userAgent  string
}

// New는 클라이언트를 만든다. app은 User-Agent에 들어갈 프로그램 이름·버전이다 (예: "NaverBooking/0.1.0").
func New(cfg Config, app string) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	host, _ := os.Hostname()
	return &Client{
		cfg:        cfg,
		http:       &http.Client{Timeout: cfg.Timeout},
		sourceHost: host,
		userAgent:  "AppWindows-" + app,
	}, nil
}

// Health는 GET /api/v1/health로 서버 상태와 인증을 확인한다.
func (c *Client) Health(ctx context.Context) (HealthResponse, error) {
	var out HealthResponse
	err := c.do(ctx, http.MethodGet, "/api/v1/health", nil, &out)
	return out, err
}

type HealthResponse struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
}

// BatchRequest는 배치 업로드 요청 본문이다. Items의 각 항목은 uid로 멱등 처리된다.
type BatchRequest[T any] struct {
	SourceHost string `json:"sourceHost"`
	Items      []T    `json:"items"`
}

type BatchResult struct {
	Accepted []string   `json:"accepted"` // 저장(또는 이미 있던) 항목 uid
	Rejected []Rejected `json:"rejected"` // 형식 오류 등으로 거부된 항목 — 재전송해도 같은 결과
}

type Rejected struct {
	UID   string `json:"uid"`
	Error string `json:"error"`
}

// PostBatch는 items를 path로 올린다. 호출하는 쪽이 MaxBatchItems 이하로 나눠 보낸다.
func PostBatch[T any](ctx context.Context, c *Client, path string, items []T) (BatchResult, error) {
	if len(items) > MaxBatchItems {
		return BatchResult{}, fmt.Errorf("배치가 너무 큽니다: %d개 (최대 %d)", len(items), MaxBatchItems)
	}
	var out BatchResult
	err := c.do(ctx, http.MethodPost, path, BatchRequest[T]{SourceHost: c.sourceHost, Items: items}, &out)
	return out, err
}

// APIError는 서버가 2xx가 아닌 응답을 준 경우다.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("API 오류 %d: %s", e.Status, e.Message) }

// Retryable은 다시 보내면 성공할 수 있는 오류인지(서버 오류·과부하) 알려 준다.
func (e *APIError) Retryable() bool { return e.Status == 429 || e.Status >= 500 }

// DoJSON은 계약의 배치 형식을 따르지 않는 엔드포인트를 부를 때 쓴다.
// path에는 쿼리 문자열을 붙일 수 있다. body가 nil이면 본문 없이, out이 nil이면 응답을 버린다.
func (c *Client) DoJSON(ctx context.Context, method, path string, body, out any) error {
	return c.do(ctx, method, path, body, out)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("API 접속 실패 (%s): %w", c.cfg.BaseURL, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(data))
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return &APIError{Status: resp.StatusCode, Message: msg}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("API 응답 형식 오류: %w", err)
	}
	return nil
}
