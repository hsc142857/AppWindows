package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenStoreAt(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func states(t *testing.T, st *Store, partner string) (map[string]LocalReservation, Counts) {
	t.Helper()
	list, counts, err := st.List(partner)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]LocalReservation{}
	for _, r := range list {
		m[r.BookingID] = r
	}
	return m, counts
}

func TestStoreUploadMarksSentOnlyAfterServerAck(t *testing.T) {
	st := openTestStore(t)
	f, cfg := newFakeVisitAPI(t)
	ctx := context.Background()

	sum, err := st.SaveScraped("p1", []Reservation{reservation("A"), reservation("B")})
	if err != nil || sum.New != 2 {
		t.Fatalf("save = %+v, %v", sum, err)
	}
	m, c := states(t, st, "p1")
	if c.Pending != 2 || c.Sent != 0 || m["A"].State != StatePending {
		t.Fatalf("before upload: %+v %+v", c, m["A"])
	}
	up, err := st.UploadPending(ctx, cfg, "p1")
	if err != nil || up.Sent != 2 || up.Remaining != 0 || up.Error != "" {
		t.Fatalf("upload = %+v, %v", up, err)
	}
	m, c = states(t, st, "p1")
	if c.Sent != 2 || m["A"].State != StateSent || m["A"].SentResult != "created" || m["A"].SentAt == "" {
		t.Fatalf("after upload: %+v %+v", c, m["A"])
	}

	// 같은 내용을 다시 긁어 오면 다시 보내지 않는다.
	posts := f.posts
	sum, _ = st.SaveScraped("p1", []Reservation{reservation("A")})
	up, _ = st.UploadPending(ctx, cfg, "p1")
	if sum.Unchanged != 1 || up.Sent != 0 || f.posts != posts {
		t.Fatalf("unchanged: save=%+v upload=%+v posts %d→%d", sum, up, posts, f.posts)
	}

	// 내용이 바뀌면 전송 완료에서 빠지고(미전송), 보내면 다시 완료.
	changed := reservation("A")
	changed.Request = "체인 점검 추가"
	sum, _ = st.SaveScraped("p1", []Reservation{changed})
	m, _ = states(t, st, "p1")
	if sum.Changed != 1 || m["A"].State != StatePending || !m["A"].EverSent {
		t.Fatalf("changed: %+v %+v", sum, m["A"])
	}
	up, _ = st.UploadPending(ctx, cfg, "p1")
	m, _ = states(t, st, "p1")
	if up.Sent != 1 || m["A"].State != StateSent || m["A"].SentResult != "updated" {
		t.Fatalf("resend changed: %+v %+v", up, m["A"])
	}
}

func TestStoreRejected(t *testing.T) {
	st := openTestStore(t)
	f, cfg := newFakeVisitAPI(t)
	ctx := context.Background()

	bad := reservation("BAD")
	bad.Phone = "02-123-4567" // 사전 검증에서 거부
	cancelMissing := reservation("GONE")
	cancelMissing.Status = "예약자 취소" // 서버에 없는 예약 취소 → 서버가 not_found
	st.SaveScraped("p1", []Reservation{bad, cancelMissing, reservation("OK")})

	up, err := st.UploadPending(ctx, cfg, "p1")
	if err != nil || up.Sent != 1 || up.Rejected != 2 || up.Remaining != 0 {
		t.Fatalf("upload = %+v, %v", up, err)
	}
	m, c := states(t, st, "p1")
	if c.Rejected != 2 || m["BAD"].State != StateRejected || m["BAD"].RejectError == "" || m["GONE"].RejectError != "not_found" {
		t.Fatalf("rejected: %+v %+v %+v", c, m["BAD"], m["GONE"])
	}
	// 거부된 건은 저절로 다시 보내지 않는다.
	posts := f.posts
	if up, _ := st.UploadPending(ctx, cfg, "p1"); up.Sent != 0 || f.posts != posts {
		t.Fatalf("rejected must not be resent: %+v", up)
	}
	// 내용을 고쳐 다시 긁어 오면 거부가 풀리고 보낸다.
	bad.Phone = "010-2222-3333"
	st.SaveScraped("p1", []Reservation{bad})
	if up, _ := st.UploadPending(ctx, cfg, "p1"); up.Sent != 1 {
		t.Fatalf("fixed item should be sent: %+v", up)
	}
	// "다시 전송": 서버 쪽 데이터를 고친 뒤
	f.visits["GONE"] = reservation("GONE").ToImportItem()
	if n, _ := st.RetryRejected("p1"); n != 1 {
		t.Fatalf("RetryRejected = %d", n)
	}
	up, _ = st.UploadPending(ctx, cfg, "p1")
	m, _ = states(t, st, "p1")
	if up.Sent != 1 || m["GONE"].State != StateSent || m["GONE"].SentResult != "cancelled" {
		t.Fatalf("retry: %+v %+v", up, m["GONE"])
	}
}

func TestStoreServerDownKeepsPending(t *testing.T) {
	st := openTestStore(t)
	f, cfg := newFakeVisitAPI(t)
	ctx := context.Background()
	st.SaveScraped("p1", []Reservation{reservation("A")})

	f.fail = true
	up, err := st.UploadPending(ctx, cfg, "p1")
	if err != nil || up.Sent != 0 || up.Remaining != 1 || up.Error == "" {
		t.Fatalf("server down = %+v, %v", up, err)
	}
	m, c := states(t, st, "p1")
	if m["A"].State != StatePending || m["A"].LastError == "" || c.Failed != 1 || c.Sent != 0 {
		t.Fatalf("after failure: %+v %+v", c, m["A"])
	}
	f.fail = false
	up, _ = st.UploadPending(ctx, cfg, "p1")
	m, c = states(t, st, "p1")
	if up.Sent != 1 || m["A"].State != StateSent || m["A"].LastError != "" || c.Failed != 0 {
		t.Fatalf("after recovery: %+v %+v %+v", up, c, m["A"])
	}
}

func TestStoreChangedDuringSendStaysPending(t *testing.T) {
	st := openTestStore(t)
	f, cfg := newFakeVisitAPI(t)
	ctx := context.Background()
	st.SaveScraped("p1", []Reservation{reservation("A")})
	// 서버가 처리하는 사이에 새 내용이 긁혀 들어온다.
	f.onPost = func() {
		r := reservation("A")
		r.Request = "도중에 바뀜"
		st.SaveScraped("p1", []Reservation{r})
	}
	up, _ := st.UploadPending(ctx, cfg, "p1")
	f.onPost = nil
	m, _ := states(t, st, "p1")
	if up.Sent != 0 || m["A"].State != StatePending {
		t.Fatalf("changed during send must stay unsent: %+v %+v", up, m["A"])
	}
	if up, _ := st.UploadPending(ctx, cfg, "p1"); up.Sent != 1 {
		t.Fatalf("next upload sends new content: %+v", up)
	}
}

func TestStoreOtherPartner(t *testing.T) {
	st := openTestStore(t)
	f, cfg := newFakeVisitAPI(t)
	st.SaveScraped("p1", []Reservation{reservation("A")})
	// 다른 업체(p2) 키로 바뀌면 p1에서 긁은 예약은 보내지 않고 따로 보여 준다.
	up, err := st.UploadPending(context.Background(), cfg, "p2")
	if err != nil || up.Sent != 0 || f.posts != 0 {
		t.Fatalf("other partner must not be sent: %+v, %v", up, err)
	}
	m, c := states(t, st, "p2")
	if m["A"].State != StateOtherPartner || c.OtherPartner != 1 || c.Pending != 0 {
		t.Fatalf("other partner state: %+v %+v", c, m["A"])
	}
}

func TestStoreSaveValidation(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.SaveScraped("", []Reservation{reservation("A")}); err == nil {
		t.Fatal("unknown partner must fail")
	}
	sum, err := st.SaveScraped("p1", []Reservation{{BookingID: " "}, reservation("A")})
	if err != nil || sum.Skipped != 1 || sum.New != 1 {
		t.Fatalf("save = %+v, %v", sum, err)
	}
	st.SetClock(func() time.Time { return time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC) })
	st.SaveScraped("p1", []Reservation{reservation("A")})
	m, _ := states(t, st, "p1")
	if m["A"].LastSeenAt != "2026-10-19T00:00:00Z" {
		t.Fatalf("last seen = %q", m["A"].LastSeenAt)
	}
}
