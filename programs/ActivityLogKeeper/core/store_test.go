package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"appwindows/shared/apiclient"
)

func openTestStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	t.Setenv("ACTIVITYLOGKEEPER_HOME", t.TempDir())
	st, err := OpenStoreAt(filepath.Join(Home(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC)
	st.SetClock(func() time.Time { return now })
	return st, &now
}

func logCapture(id int64, rows ...[]string) Capture {
	return Capture{TargetID: id, RequestedURL: "https://app.example.com/logs", FinalURL: "https://app.example.com/logs",
		Title: "활동 로그", Headers: []string{"일시", "작업", "사용자"}, Rows: rows}
}

func TestTargets(t *testing.T) {
	st, _ := openTestStore(t)
	a, err := st.AddTarget(" https://a.com/logs ", "A", " table#log ")
	if err != nil {
		t.Fatal(err)
	}
	if a.URL != "https://a.com/logs" || a.Name != "A" || a.Selector != "table#log" || !a.Enabled {
		t.Fatalf("unexpected target %+v", a)
	}
	if _, err := st.AddTarget("https://a.com/logs", "dup", ""); err == nil {
		t.Fatal("duplicate URL should fail")
	}
	if _, err := st.AddTarget("javascript:alert(1)", "", ""); err == nil {
		t.Fatal("non-http URL should fail")
	}
	if err := st.SetTargetSelector(a.ID, ""); err != nil {
		t.Fatal(err)
	}
	b, _ := st.AddTarget("https://b.com", "", "")
	if err := st.SetTargetEnabled(b.ID, false); err != nil {
		t.Fatal(err)
	}
	enabled, _ := st.Targets(true)
	if len(enabled) != 1 || enabled[0].ID != a.ID || enabled[0].Selector != "" {
		t.Fatalf("enabled targets = %+v", enabled)
	}
	if err := st.RemoveTarget(999); err == nil {
		t.Fatal("removing missing target should fail")
	}
}

func TestSaveCaptureDedupesRows(t *testing.T) {
	st, now := openTestStore(t)
	tg, _ := st.AddTarget("https://app.example.com/logs", "업무앱", "")

	r1 := []string{"2026-10-19 09:00", "  로그인 ", "kim"}
	r2 := []string{"2026-10-19 09:05", "주문 취소", "kim"}
	res, err := st.SaveCapture(logCapture(tg.ID, r1, r2, []string{"", " "}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusOK || res.RowCount != 2 || res.NewRows != 2 {
		t.Fatalf("first save = %+v", res)
	}

	// 다음 수집: 새 행이 위에 하나 생기고 기존 두 행은 그대로 보인다 (공백 차이는 같은 행).
	*now = now.Add(10 * time.Minute)
	r0 := []string{"2026-10-19 09:12", "결제 승인", "lee"}
	res, err = st.SaveCapture(logCapture(tg.ID, r0, []string{"2026-10-19 09:00", "로그인", "kim"}, r2))
	if err != nil {
		t.Fatal(err)
	}
	if res.RowCount != 3 || res.NewRows != 1 {
		t.Fatalf("second save = %+v", res)
	}

	list, _ := st.Entries(EntryFilter{})
	if len(list) != 3 {
		t.Fatalf("entries = %d", len(list))
	}
	first := list[0]
	if first.Cells[1] != "로그인" || first.TargetName != "업무앱" || first.Headers[0] != "일시" {
		t.Fatalf("first entry = %+v", first)
	}
	if first.FirstSeenAt == first.LastSeenAt {
		t.Fatal("re-seen row should update last_seen_at")
	}
	if first.UID != EntryUID(tg.URL, RowHash(first.Cells), 1) {
		t.Fatal("uid should be deterministic")
	}
	if n, _ := st.UnsyncedCount(); n != 3 {
		t.Fatalf("unsynced = %d", n)
	}
	caps, _ := st.Captures(10, "")
	if len(caps) != 2 || caps[0].NewRows != 1 || caps[1].RowCount != 2 {
		t.Fatalf("captures = %+v", caps)
	}
}

func TestSaveCaptureDuplicateRowsInOnePage(t *testing.T) {
	st, _ := openTestStore(t)
	tg, _ := st.AddTarget("https://app.example.com/logs", "", "")
	same := []string{"09:00", "새로고침"}
	res, _ := st.SaveCapture(logCapture(tg.ID, same, same, same))
	if res.NewRows != 3 {
		t.Fatalf("identical rows on one page should be kept separately: %+v", res)
	}
	res, _ = st.SaveCapture(logCapture(tg.ID, same, same))
	if res.NewRows != 0 {
		t.Fatalf("fewer identical rows should not add: %+v", res)
	}
	res, _ = st.SaveCapture(logCapture(tg.ID, same, same, same, same))
	if res.NewRows != 1 {
		t.Fatalf("one more identical row should add one: %+v", res)
	}
}

func TestSaveCaptureStatuses(t *testing.T) {
	st, _ := openTestStore(t)
	tg, _ := st.AddTarget("https://app.example.com/logs", "", "")

	login := logCapture(tg.ID, []string{"x"})
	login.FinalURL = "https://app.example.com/login?next=/logs"
	empty := logCapture(tg.ID)
	failed := logCapture(tg.ID, []string{"x"})
	failed.Error = "페이지 로딩 시간 초과"

	for _, c := range []struct {
		capture Capture
		want    string
	}{{login, StatusLoginRequired}, {empty, StatusEmpty}, {failed, StatusError}} {
		res, err := st.SaveCapture(c.capture)
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != c.want || res.NewRows != 0 {
			t.Fatalf("status = %+v, want %s", res, c.want)
		}
	}
	if n, _ := st.UnsyncedCount(); n != 0 {
		t.Fatal("non-ok captures must not create entries")
	}
	s, _ := st.Status()
	if len(s.NeedAttention) != 1 || s.NeedAttention[0].LastStatus != StatusError {
		t.Fatalf("need attention = %+v", s.NeedAttention)
	}
}

func TestEntriesFilter(t *testing.T) {
	st, now := openTestStore(t)
	a, _ := st.AddTarget("https://a.com/logs", "A", "")
	b, _ := st.AddTarget("https://b.com/logs", "B", "")
	st.SaveCapture(Capture{TargetID: a.ID, RequestedURL: a.URL, FinalURL: a.URL, Rows: [][]string{{"a1"}}})
	*now = now.Add(24 * time.Hour)
	st.SaveCapture(Capture{TargetID: b.ID, RequestedURL: b.URL, FinalURL: b.URL, Rows: [][]string{{"b1"}}})
	st.SaveCapture(Capture{TargetID: a.ID, RequestedURL: a.URL, FinalURL: a.URL, Rows: [][]string{{"a2"}, {"a1"}}})

	onlyA, _ := st.Entries(EntryFilter{TargetURL: a.URL})
	if len(onlyA) != 2 {
		t.Fatalf("target filter = %d", len(onlyA))
	}
	day2, _ := st.Entries(EntryFilter{From: FormatTime(*now)})
	if len(day2) != 2 {
		t.Fatalf("from filter = %d", len(day2))
	}
	newest, _ := st.Entries(EntryFilter{Newest: true, Limit: 1})
	if len(newest) != 1 || newest[0].Cells[0] != "a2" {
		t.Fatalf("newest = %+v", newest)
	}
	// 대상을 지워도 모은 로그는 남는다.
	if err := st.RemoveTarget(a.ID); err != nil {
		t.Fatal(err)
	}
	all, _ := st.Entries(EntryFilter{})
	if len(all) != 3 {
		t.Fatalf("entries after target removal = %d", len(all))
	}
}

func TestWriteCSV(t *testing.T) {
	st, _ := openTestStore(t)
	tg, _ := st.AddTarget("https://app.example.com/logs", "업무앱", "")
	st.SaveCapture(logCapture(tg.ID, []string{"09:00", "로그인, 성공", "kim"}))
	list, _ := st.Entries(EntryFilter{})
	var buf bytes.Buffer
	if err := WriteCSV(&buf, list, time.UTC); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "\xef\xbb\xbf번호,대상,URL,처음 확인") {
		t.Fatalf("csv header = %q", out[:60])
	}
	if !strings.Contains(out, ",일시,작업,사용자\n") || !strings.Contains(out, `"로그인, 성공"`) ||
		!strings.Contains(out, "2026-10-19 06:00:00") {
		t.Fatalf("csv = %s", out)
	}
}

// fakeServer는 배치 계약대로 응답하는 테스트 서버다. reject에 든 셀 내용은 거부한다.
func fakeServer(t *testing.T, reject string, status int) (*httptest.Server, *[]EntryItem) {
	var mu sync.Mutex
	got := []EntryItem{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != EntriesBatchPath || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, `{"error":"bad"}`, 400)
			return
		}
		if status != 200 {
			w.WriteHeader(status)
			w.Write([]byte(`{"error":"down"}`))
			return
		}
		var req apiclient.BatchRequest[EntryItem]
		json.NewDecoder(r.Body).Decode(&req)
		res := apiclient.BatchResult{Accepted: []string{}, Rejected: []apiclient.Rejected{}}
		mu.Lock()
		for _, it := range req.Items {
			if len(it.Cells) > 0 && it.Cells[0] == reject {
				res.Rejected = append(res.Rejected, apiclient.Rejected{UID: it.UID, Error: "형식 오류"})
				continue
			}
			got = append(got, it)
			res.Accepted = append(res.Accepted, it.UID)
		}
		mu.Unlock()
		json.NewEncoder(w).Encode(res)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestSync(t *testing.T) {
	st, _ := openTestStore(t)
	tg, _ := st.AddTarget("https://app.example.com/logs", "", "")
	rows := [][]string{{"bad"}}
	for i := 0; i < 150; i++ {
		rows = append(rows, []string{"row", time.Duration(i).String()})
	}
	st.SaveCapture(logCapture(tg.ID, rows...))

	// 서버 장애: 아무것도 완료되지 않고 로컬에 남는다.
	down, _ := fakeServer(t, "", 503)
	res, err := Sync(context.Background(), st, apiclient.Config{BaseURL: down.URL, Token: "tok"})
	var apiErr *apiclient.APIError
	if err == nil || !errorsAs(err, &apiErr) || !apiErr.Retryable() || res.Remaining != 151 {
		t.Fatalf("down: res=%+v err=%v", res, err)
	}

	srv, got := fakeServer(t, "bad", 200)
	res, err = Sync(context.Background(), st, apiclient.Config{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 150 || res.Rejected != 1 || res.Remaining != 0 || len(*got) != 150 {
		t.Fatalf("sync = %+v, server got %d", res, len(*got))
	}
	if n, _ := st.RejectedCount(); n != 1 {
		t.Fatalf("rejected = %d", n)
	}
	if n, _ := st.ClearRejected(); n != 1 {
		t.Fatalf("clear rejected = %d", n)
	}
	if n, _ := st.UnsyncedCount(); n != 1 {
		t.Fatalf("unsynced after clear = %d", n)
	}
}
