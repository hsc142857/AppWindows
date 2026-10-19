package core

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	t.Setenv("PAGEVAULT_HOME", t.TempDir())
	st, err := OpenStoreAt(filepath.Join(Home(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC)
	st.SetClock(func() time.Time { return now })
	return st, &now
}

func TestTargets(t *testing.T) {
	st, _ := openTestStore(t)
	a, err := st.AddTarget(" https://a.com/list ", "A")
	if err != nil {
		t.Fatal(err)
	}
	if a.URL != "https://a.com/list" || a.Name != "A" || !a.Enabled {
		t.Fatalf("unexpected target %+v", a)
	}
	if _, err := st.AddTarget("https://a.com/list", "dup"); err == nil {
		t.Fatal("duplicate URL should fail")
	}
	if _, err := st.AddTarget("ftp://a.com", ""); err == nil {
		t.Fatal("non-http URL should fail")
	}
	b, _ := st.AddTarget("https://b.com", "")
	if err := st.SetTargetEnabled(b.ID, false); err != nil {
		t.Fatal(err)
	}
	enabled, _ := st.Targets(true)
	if len(enabled) != 1 || enabled[0].ID != a.ID {
		t.Fatalf("enabled targets = %+v", enabled)
	}
	if err := st.RemoveTarget(999); err == nil {
		t.Fatal("removing missing target should fail")
	}
}

func TestSettingsValidation(t *testing.T) {
	st, _ := openTestStore(t)
	s, _ := st.Settings()
	if s.IntervalSeconds != DefaultIntervalSeconds || s.LoadDelaySeconds != DefaultLoadDelaySeconds {
		t.Fatalf("defaults = %+v", s)
	}
	if err := st.SetInterval(29); err == nil {
		t.Fatal("interval below 30 should fail")
	}
	if err := st.SetInterval(30); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLoadDelay(-1); err == nil {
		t.Fatal("negative delay should fail")
	}
	if err := st.SetLoadDelay(121); err == nil {
		t.Fatal("delay above 120 should fail")
	}
	if err := st.SetLoadDelay(0); err != nil {
		t.Fatal(err)
	}
	s, _ = st.Settings()
	if s.IntervalSeconds != 30 || s.LoadDelaySeconds != 0 {
		t.Fatalf("settings = %+v", s)
	}
}

func okCapture(id int64, url, title, text, html string) Capture {
	return Capture{TargetID: id, RequestedURL: url, FinalURL: url, Title: title, Text: text, HTML: html}
}

func TestSaveCaptureDedup(t *testing.T) {
	st, now := openTestStore(t)
	tg, _ := st.AddTarget("https://a.com/list", "A")

	r1, err := st.SaveCapture(okCapture(tg.ID, tg.URL, "T", "body", "<html>token=1</html>"))
	if err != nil || !r1.Changed || r1.Status != StatusOK {
		t.Fatalf("first save = %+v, %v", r1, err)
	}
	if err := st.MarkSynced(mustUnsynced(t, st)); err != nil {
		t.Fatal(err)
	}

	// 본문이 같고 HTML만 다르면 새 행을 만들지 않는다.
	*now = now.Add(time.Minute)
	r2, err := st.SaveCapture(okCapture(tg.ID, tg.URL, "T", "body", "<html>token=2</html>"))
	if err != nil || r2.Changed || r2.SnapshotID != r1.SnapshotID {
		t.Fatalf("duplicate save = %+v, %v", r2, err)
	}
	sn, _ := st.Snapshot(r1.SnapshotID)
	if sn.LastSeenAt != FormatTime(*now) || sn.SyncedAt != "" {
		t.Fatalf("last_seen_at/synced_at not updated: %+v", sn)
	}
	if sn.HTML != "<html>token=1</html>" {
		t.Fatalf("html should keep the first capture, got %q", sn.HTML)
	}

	// 본문이 바뀌면 새 행.
	r3, _ := st.SaveCapture(okCapture(tg.ID, tg.URL, "T", "body changed", ""))
	if !r3.Changed || r3.SnapshotID == r1.SnapshotID {
		t.Fatalf("changed save = %+v", r3)
	}
	// 이전 내용으로 돌아가도 직전 행과만 비교하므로 새 행.
	r4, _ := st.SaveCapture(okCapture(tg.ID, tg.URL, "T", "body", ""))
	if !r4.Changed {
		t.Fatalf("revert should create a row: %+v", r4)
	}
	list, _ := st.Snapshots(10, tg.URL)
	if len(list) != 3 {
		t.Fatalf("want 3 snapshots, got %d", len(list))
	}
}

func TestSaveCaptureStatuses(t *testing.T) {
	st, _ := openTestStore(t)
	tg, _ := st.AddTarget("https://partner.booking.naver.com/bizes/1/list", "예약")

	login := Capture{TargetID: tg.ID, RequestedURL: tg.URL, FinalURL: "https://nid.naver.com/nidlogin.login",
		Title: "로그인", Text: "secret", HTML: "<html/>"}
	r, err := st.SaveCapture(login)
	if err != nil || r.Status != StatusLoginRequired {
		t.Fatalf("login save = %+v, %v", r, err)
	}
	sn, _ := st.Snapshot(r.SnapshotID)
	if sn.Text != "" || sn.HTML != "" || sn.ContentHash != loginRequiredHash {
		t.Fatalf("login_required must not keep text/html: %+v", sn)
	}
	// 로그인 필요가 반복되면 새 행을 만들지 않는다 (제목이 달라도).
	login.Title = "다른 제목"
	r2, _ := st.SaveCapture(login)
	if r2.Changed {
		t.Fatalf("repeated login_required should not create a row: %+v", r2)
	}

	states, _ := st.TargetStates()
	if states[0].LastStatus != StatusLoginRequired {
		t.Fatalf("target state = %+v", states[0])
	}
	status, _ := st.Status()
	if len(status.LoginRequired) != 1 {
		t.Fatalf("status.LoginRequired = %+v", status.LoginRequired)
	}

	r3, _ := st.SaveCapture(Capture{TargetID: tg.ID, RequestedURL: tg.URL, Error: "페이지 로딩 시간 초과", Text: "x"})
	if r3.Status != StatusError || !r3.Changed {
		t.Fatalf("error save = %+v", r3)
	}
	sn, _ = st.Snapshot(r3.SnapshotID)
	if sn.Error == "" || sn.Text != "" {
		t.Fatalf("error snapshot = %+v", sn)
	}
}

func TestSaveCaptureTargetRemoved(t *testing.T) {
	st, _ := openTestStore(t)
	tg, _ := st.AddTarget("https://a.com", "A")
	r, _ := st.SaveCapture(okCapture(tg.ID, tg.URL, "T", "x", ""))
	if err := st.RemoveTarget(tg.ID); err != nil {
		t.Fatal(err)
	}
	sn, err := st.Snapshot(r.SnapshotID)
	if err != nil || sn.TargetID != nil || sn.TargetName != "A" {
		t.Fatalf("snapshot after target removal = %+v, %v", sn, err)
	}
	// 없는 targetID로 와도 URL 기준으로 저장된다.
	if _, err := st.SaveCapture(okCapture(999, "https://a.com", "T", "y", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveCapture(Capture{}); err == nil {
		t.Fatal("empty requestedURL should fail")
	}
}

func TestMarkSyncedOnlyIfUnchanged(t *testing.T) {
	st, now := openTestStore(t)
	tg, _ := st.AddTarget("https://a.com", "")
	st.SaveCapture(okCapture(tg.ID, tg.URL, "T", "x", ""))
	sent := mustUnsynced(t, st)

	// 보내는 사이에 같은 내용이 다시 수집되어 last_seen_at이 바뀐 경우
	*now = now.Add(time.Second)
	st.SaveCapture(okCapture(tg.ID, tg.URL, "T", "x", ""))

	if err := st.MarkSynced(sent); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.UnsyncedCount(); n != 1 {
		t.Fatalf("row updated during send must stay unsynced, unsynced=%d", n)
	}
	if err := st.MarkSynced(mustUnsynced(t, st)); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.UnsyncedCount(); n != 0 {
		t.Fatalf("unsynced=%d", n)
	}
}

func mustUnsynced(t *testing.T, st *Store) []Snapshot {
	t.Helper()
	rows, err := st.UnsyncedAfter(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestFilepathToURI(t *testing.T) {
	cases := map[string]string{
		`C:\Users\a b\AppData\Local\PageVault\pagevault.sqlite`: "/C:/Users/a%20b/AppData/Local/PageVault/pagevault.sqlite",
		"/tmp/x.sqlite": "/tmp/x.sqlite",
	}
	for in, want := range cases {
		if got := filepath2URI(in); got != want {
			t.Errorf("filepath2URI(%q) = %q, want %q", in, got, want)
		}
	}
}
