package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConfigValidate(t *testing.T) {
	ok := []Config{
		{BaseURL: "https://api.example.com", Token: "t"},
		{BaseURL: "http://localhost:8080/", Token: "t"},
		{BaseURL: "http://127.0.0.1:8080", Token: "t"},
		{BaseURL: "http://172.30.1.58:3000", Token: "t"}, // 같은 공유기의 Mac
		{BaseURL: "http://10.211.55.2:3000", Token: "t"}, // Parallels 호스트
		{BaseURL: "http://192.168.0.10:3000", Token: "t"},
	}
	for _, c := range ok {
		if err := c.Validate(); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	bad := []Config{
		{BaseURL: "http://api.example.com", Token: "t"},
		{BaseURL: "http://8.8.8.8:3000", Token: "t"},     // 공인 IP는 https만
		{BaseURL: "http://172.32.0.1:3000", Token: "t"},  // 172.16/12 밖
		{BaseURL: "http://mymac.local:3000", Token: "t"}, // 이름은 IP로 바꿔 입력
		{BaseURL: "ftp://x", Token: "t"},
		{BaseURL: "https://api.example.com", Token: " "},
		{BaseURL: "", Token: "t"},
	}
	for _, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%+v: expected error", c)
		}
	}
}

type item struct {
	UID string `json:"uid"`
}

func TestPostBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid token"})
			return
		}
		switch r.URL.Path {
		case "/api/v1/health":
			json.NewEncoder(w).Encode(HealthResponse{OK: true, Version: "test"})
		case "/api/v1/things:batch":
			var req BatchRequest[item]
			json.NewDecoder(r.Body).Decode(&req)
			res := BatchResult{}
			for _, it := range req.Items {
				if it.UID == "bad" {
					res.Rejected = append(res.Rejected, Rejected{UID: it.UID, Error: "invalid"})
				} else {
					res.Accepted = append(res.Accepted, it.UID)
				}
			}
			json.NewEncoder(w).Encode(res)
		case "/api/v1/busy:batch":
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	c, err := New(Config{BaseURL: srv.URL, Token: "secret"}, "Test/0")
	if err != nil {
		t.Fatal(err)
	}
	if h, err := c.Health(ctx); err != nil || !h.OK {
		t.Fatalf("health = %+v, %v", h, err)
	}
	res, err := PostBatch(ctx, c, "/api/v1/things:batch", []item{{"a"}, {"bad"}, {"b"}})
	if err != nil || len(res.Accepted) != 2 || len(res.Rejected) != 1 {
		t.Fatalf("batch = %+v, %v", res, err)
	}
	_, err = PostBatch(ctx, c, "/api/v1/busy:batch", []item{{"a"}})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.Retryable() {
		t.Fatalf("503 should be retryable APIError, got %v", err)
	}

	wrong, _ := New(Config{BaseURL: srv.URL, Token: "nope"}, "Test/0")
	_, err = wrong.Health(ctx)
	if !errors.As(err, &apiErr) || apiErr.Status != 401 || apiErr.Message != "invalid token" || apiErr.Retryable() {
		t.Fatalf("401 = %v", err)
	}
	if _, err := PostBatch(ctx, c, "/x", make([]item, MaxBatchItems+1)); err == nil {
		t.Fatal("oversized batch should fail")
	}
}

func TestMaskedToken(t *testing.T) {
	cases := map[string]string{"": "****", "short": "****", "msvi_abcdefghWXYZ": "msvi_****WXYZ", "abcdefghijklmnop": "****mnop"}
	for in, want := range cases {
		if got := (Config{Token: in}).MaskedToken(); got != want {
			t.Errorf("MaskedToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConfigStore(t *testing.T) {
	path := t.TempDir() + "/sub/api.json"
	if _, ok, err := LoadConfig(path); ok || err != nil {
		t.Fatalf("missing file: ok=%v err=%v", ok, err)
	}
	if err := SaveConfig(path, Config{BaseURL: "ftp://x", Token: "k"}); err == nil {
		t.Fatal("invalid config must not be saved")
	}
	want := Config{BaseURL: "http://localhost:3000", Token: "msvi_test"}
	if err := SaveConfig(path, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadConfig(path)
	if err != nil || !ok || got.BaseURL != want.BaseURL || got.Token != want.Token {
		t.Fatalf("load = %+v ok=%v err=%v", got, ok, err)
	}
	if err := ClearConfig(path); err != nil {
		t.Fatal(err)
	}
	if err := ClearConfig(path); err != nil {
		t.Fatal("clear twice should succeed")
	}
}
