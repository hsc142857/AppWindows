package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"appwindows/shared/apiclient"
	_ "modernc.org/sqlite"
)

func TestAPIConfigSaveLoad(t *testing.T) {
	t.Setenv("PAGEVAULT_HOME", t.TempDir())
	if _, ok, err := LoadAPIConfig(); ok || err != nil {
		t.Fatalf("empty load: ok=%v err=%v", ok, err)
	}
	if _, err := NewAPIConfig("http://api.example.com", "t"); err == nil {
		t.Fatal("plain http to a remote host should fail")
	}
	c, err := NewAPIConfig(" https://api.example.com/ ", " secret-token ")
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL != "https://api.example.com" || c.Token != "secret-token" {
		t.Fatalf("normalized config = %+v", c)
	}
	if err := SaveAPIConfig(c); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(APIConfigPath()); fi.Mode().Perm() != 0o600 {
		t.Fatalf("api.json mode = %v", fi.Mode().Perm())
	}
	got, ok, err := LoadAPIConfig()
	if err != nil || !ok || got.BaseURL != c.BaseURL || got.Token != c.Token {
		t.Fatalf("load = %+v ok=%v err=%v", got, ok, err)
	}
	summary, configured := APISummary()
	if !configured || strings.Contains(summary, "secret-token") {
		t.Fatalf("summary must mask the token: %q", summary)
	}
	if err := ClearAPIConfig(); err != nil {
		t.Fatal(err)
	}
	if err := ClearAPIConfig(); err != nil {
		t.Fatal("clear twice should succeed")
	}
	os.WriteFile(legacyPostgresPath(), []byte("{}"), 0o600)
	if summary, _ := APISummary(); !strings.Contains(summary, "postgres.json") {
		t.Fatalf("legacy postgres.json should be mentioned: %q", summary)
	}
}

// fakeAPI는 계약(docs/web-api.md)대로 동작하는 테스트 서버다.
type fakeAPI struct {
	mu       sync.Mutex
	items    map[string]SnapshotItem
	requests int
	reject   map[string]bool // 이 targetURL의 항목은 거부
	fail     bool            // true면 503
	onBatch  func()          // 요청을 받은 직후 실행 (보내는 사이의 변경 흉내)
}

func newFakeAPI(t *testing.T) (*fakeAPI, apiclient.Config) {
	f := &fakeAPI{items: map[string]SnapshotItem{}, reject: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/health":
			json.NewEncoder(w).Encode(apiclient.HealthResponse{OK: true, Version: "fake"})
		case SnapshotsBatchPath:
			var req apiclient.BatchRequest[SnapshotItem]
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			f.requests++
			fail, onBatch := f.fail, f.onBatch
			f.onBatch = nil
			res := apiclient.BatchResult{}
			if !fail {
				for _, it := range req.Items {
					if f.reject[it.TargetURL] {
						res.Rejected = append(res.Rejected, apiclient.Rejected{UID: it.UID, Error: "targetURL not allowed"})
						continue
					}
					f.items[it.UID] = it // uid 멱등: 같은 uid는 덮어쓰기
					res.Accepted = append(res.Accepted, it.UID)
				}
			}
			f.mu.Unlock()
			if onBatch != nil {
				onBatch()
			}
			if fail {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			json.NewEncoder(w).Encode(res)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return f, apiclient.Config{BaseURL: srv.URL, Token: "test-token"}
}

func TestTestAPI(t *testing.T) {
	_, cfg := newFakeAPI(t)
	if v, err := TestAPI(context.Background(), cfg); err != nil || v != "fake" {
		t.Fatalf("TestAPI = %q, %v", v, err)
	}
	cfg.Token = "wrong"
	if _, err := TestAPI(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("wrong token should fail with 401: %v", err)
	}
}

func TestSyncUploadsInBatches(t *testing.T) {
	st, now := openTestStore(t)
	f, cfg := newFakeAPI(t)
	tg, _ := st.AddTarget("https://a.com", "A")
	for i := 0; i < 250; i++ {
		*now = now.Add(time.Second)
		st.SaveCapture(okCapture(tg.ID, tg.URL, "T", strings.Repeat("x", i), "<html/>"))
	}
	res, err := Sync(context.Background(), st, cfg)
	if err != nil || res.Sent != 250 || res.Remaining != 0 || !res.APIConfigured {
		t.Fatalf("sync = %+v, %v", res, err)
	}
	if f.requests != 3 || len(f.items) != 250 {
		t.Fatalf("requests=%d items=%d, want 3 batches and 250 items", f.requests, len(f.items))
	}

	// 같은 내용 재수집 → last_seen_at만 바뀐 같은 uid를 다시 보낸다.
	*now = now.Add(time.Second)
	r, _ := st.SaveCapture(okCapture(tg.ID, tg.URL, "T", strings.Repeat("x", 249), "<html/>"))
	res, err = Sync(context.Background(), st, cfg)
	if err != nil || res.Sent != 1 || len(f.items) != 250 {
		t.Fatalf("resync = %+v, %v, items=%d", res, err, len(f.items))
	}
	sn, _ := st.Snapshot(r.SnapshotID)
	if f.items[sn.UID].LastSeenAt != FormatTime(*now) {
		t.Fatalf("server lastSeenAt = %s, want %s", f.items[sn.UID].LastSeenAt, FormatTime(*now))
	}
}

func TestSyncItemFields(t *testing.T) {
	st, _ := openTestStore(t)
	f, cfg := newFakeAPI(t)
	tg, _ := st.AddTarget("https://a.com/list", "A")
	ok, _ := st.SaveCapture(okCapture(tg.ID, tg.URL, "제목", "본문", "<html>본문</html>"))
	login, _ := st.SaveCapture(Capture{TargetID: tg.ID, RequestedURL: tg.URL, FinalURL: "https://a.com/login"})
	if _, err := Sync(context.Background(), st, cfg); err != nil {
		t.Fatal(err)
	}
	okSn, _ := st.Snapshot(ok.SnapshotID)
	it := f.items[okSn.UID]
	if it.Text == nil || *it.Text != "본문" || it.HTML == nil || it.TargetName != "A" || it.Status != StatusOK {
		t.Fatalf("ok item = %+v", it)
	}
	loginSn, _ := st.Snapshot(login.SnapshotID)
	if it := f.items[loginSn.UID]; it.Text != nil || it.HTML != nil || it.Status != StatusLoginRequired {
		t.Fatalf("login_required item must have null text/html: %+v", it)
	}
}

func TestSyncRejectedExcluded(t *testing.T) {
	st, _ := openTestStore(t)
	f, cfg := newFakeAPI(t)
	good, _ := st.AddTarget("https://good.com", "")
	bad, _ := st.AddTarget("https://bad.com", "")
	st.SaveCapture(okCapture(good.ID, good.URL, "T", "x", ""))
	r, _ := st.SaveCapture(okCapture(bad.ID, bad.URL, "T", "y", ""))
	f.reject[bad.URL] = true

	res, err := Sync(context.Background(), st, cfg)
	if err != nil || res.Sent != 1 || res.Rejected != 1 || res.Remaining != 0 {
		t.Fatalf("sync = %+v, %v", res, err)
	}
	sn, _ := st.Snapshot(r.SnapshotID)
	if sn.SyncError != "targetURL not allowed" || sn.SyncedAt != "" {
		t.Fatalf("rejected snapshot = %+v", sn)
	}
	status, _ := st.Status()
	if status.Rejected != 1 || status.Unsynced != 0 {
		t.Fatalf("status = %+v", status)
	}
	// 거부된 행은 다시 보내지 않는다.
	before := f.requests
	res, _ = Sync(context.Background(), st, cfg)
	if res.Sent != 0 || f.requests != before {
		t.Fatalf("rejected rows must not be resent: %+v requests %d→%d", res, before, f.requests)
	}
	// 서버를 고친 뒤 다시 보내기
	delete(f.reject, bad.URL)
	if n, _ := st.ClearRejected(); n != 1 {
		t.Fatalf("ClearRejected = %d", n)
	}
	res, err = Sync(context.Background(), st, cfg)
	if err != nil || res.Sent != 1 || res.Rejected != 0 {
		t.Fatalf("retry = %+v, %v", res, err)
	}
}

func TestSyncServerErrorKeepsData(t *testing.T) {
	st, _ := openTestStore(t)
	f, cfg := newFakeAPI(t)
	tg, _ := st.AddTarget("https://a.com", "")
	st.SaveCapture(okCapture(tg.ID, tg.URL, "T", "x", ""))
	f.fail = true
	res, err := Sync(context.Background(), st, cfg)
	if err == nil || !strings.Contains(err.Error(), "503") || res.Remaining != 1 {
		t.Fatalf("sync on 503 = %+v, %v", res, err)
	}
	f.fail = false
	if res, err := Sync(context.Background(), st, cfg); err != nil || res.Sent != 1 {
		t.Fatalf("sync after recovery = %+v, %v", res, err)
	}
}

func TestSyncUpdatedDuringSendIsResent(t *testing.T) {
	st, now := openTestStore(t)
	f, cfg := newFakeAPI(t)
	tg, _ := st.AddTarget("https://a.com", "")
	st.SaveCapture(okCapture(tg.ID, tg.URL, "T", "x", ""))
	// 서버가 받은 직후, 응답 전에 같은 내용이 다시 수집된다.
	f.onBatch = func() {
		*now = now.Add(time.Second)
		st.SaveCapture(okCapture(tg.ID, tg.URL, "T", "x", ""))
	}
	res, err := Sync(context.Background(), st, cfg)
	if err != nil || res.Remaining != 1 {
		t.Fatalf("row updated during send must stay unsynced: %+v, %v", res, err)
	}
	res, err = Sync(context.Background(), st, cfg)
	if err != nil || res.Sent != 1 || res.Remaining != 0 {
		t.Fatalf("second sync = %+v, %v", res, err)
	}
}

func TestSyncBatchSizeLimit(t *testing.T) {
	st, now := openTestStore(t)
	f, cfg := newFakeAPI(t)
	tg, _ := st.AddTarget("https://a.com", "")
	big := strings.Repeat("h", 3<<20) // 3MB HTML × 5건 → 8MB 상한으로 나눠 보내야 한다
	for i := 0; i < 5; i++ {
		*now = now.Add(time.Second)
		st.SaveCapture(okCapture(tg.ID, tg.URL, "T", strings.Repeat("x", i+1), big))
	}
	res, err := Sync(context.Background(), st, cfg)
	if err != nil || res.Sent != 5 {
		t.Fatalf("sync = %+v, %v", res, err)
	}
	if f.requests != 3 {
		t.Fatalf("requests = %d, want 3 (2+2+1 by size)", f.requests)
	}
}

func TestSyncConfiguredWithoutConfig(t *testing.T) {
	st, _ := openTestStore(t)
	tg, _ := st.AddTarget("https://a.com", "")
	st.SaveCapture(okCapture(tg.ID, tg.URL, "T", "x", ""))
	res, err := SyncConfigured(context.Background(), st)
	if err != nil || res.APIConfigured || res.Remaining != 1 {
		t.Fatalf("sync without config = %+v, %v", res, err)
	}
}

// Web API 전환 전에 만든 DB(sync_error 컬럼 없음)를 열면 컬럼이 추가된다.
func TestMigrateAddsSyncError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE snapshots (
		id INTEGER PRIMARY KEY, uid TEXT NOT NULL UNIQUE, target_id INTEGER, target_url TEXT NOT NULL,
		target_name TEXT NOT NULL DEFAULT '', final_url TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '',
		text TEXT, html TEXT, content_hash TEXT NOT NULL, status TEXT NOT NULL, error TEXT NOT NULL DEFAULT '',
		captured_at TEXT NOT NULL, last_seen_at TEXT NOT NULL, synced_at TEXT);
		INSERT INTO snapshots(uid, target_url, content_hash, status, captured_at, last_seen_at)
		VALUES('u1', 'https://a.com', 'h', 'ok', '2026-10-19T00:00:00.000000Z', '2026-10-19T00:00:00.000000Z');`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	st, err := OpenStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if n, err := st.UnsyncedCount(); err != nil || n != 1 {
		t.Fatalf("unsynced after migration = %d, %v", n, err)
	}
	// 두 번 열어도 문제없다.
	st.Close()
	st2, err := OpenStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	st2.Close()
}
