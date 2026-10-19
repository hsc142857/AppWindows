package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func frame(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteMessage(&buf, v); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFramingRoundTrip(t *testing.T) {
	msg := map[string]any{"id": 1, "type": "ping", "text": "한글 텍스트"}
	b := frame(t, msg)
	if n := binary.LittleEndian.Uint32(b[:4]); int(n) != len(b)-4 {
		t.Fatalf("length header %d, body %d", n, len(b)-4)
	}
	r := bytes.NewReader(append(b, frame(t, map[string]any{"id": 2})...))
	got, err := ReadMessage(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(got, &m)
	if m["text"] != "한글 텍스트" {
		t.Fatalf("got %v", m)
	}
	if _, err := ReadMessage(r); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMessage(r); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF at boundary, got %v", err)
	}
}

func TestFramingErrors(t *testing.T) {
	if _, err := ReadMessage(bytes.NewReader([]byte{1, 0})); err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("truncated header: %v", err)
	}
	if _, err := ReadMessage(bytes.NewReader([]byte{10, 0, 0, 0, '{'})); err == nil {
		t.Fatal("truncated body should fail")
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], MaxIncomingMessage+1)
	if _, err := ReadMessage(bytes.NewReader(hdr[:])); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("oversized: %v", err)
	}
	big := strings.Repeat("x", MaxOutgoingMessage)
	if err := WriteMessage(io.Discard, map[string]string{"x": big}); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("oversized outgoing: %v", err)
	}
}

func readAll(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	r := bytes.NewReader(b)
	var out []map[string]any
	for {
		msg, err := ReadMessage(r)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(msg, &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
}

func TestHostServe(t *testing.T) {
	st, _ := openTestStore(t)
	tg, _ := st.AddTarget("https://a.com/list", "A")
	off, _ := st.AddTarget("https://b.com", "B")
	st.SetTargetEnabled(off.ID, false)
	st.SetInterval(45)

	var in bytes.Buffer
	in.Write(frame(t, map[string]any{"id": 1, "type": "ping"}))
	in.Write(frame(t, map[string]any{"id": 2, "type": "config"}))
	in.Write(frame(t, map[string]any{"id": 3, "type": "save", "capture": okCapture(tg.ID, tg.URL, "T", "body", "<html/>")}))
	in.Write(frame(t, map[string]any{"id": 4, "type": "save", "capture": okCapture(tg.ID, tg.URL, "T", "body", "<html/>")}))
	in.Write(frame(t, map[string]any{"id": 5, "type": "save"}))
	in.Write(frame(t, map[string]any{"id": 6, "type": "sync"}))
	in.Write(frame(t, map[string]any{"id": 7, "type": "nope"}))
	raw := []byte("not json")
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(raw)))
	in.Write(hdr[:])
	in.Write(raw)

	var out bytes.Buffer
	syncCalled := false
	h := &Host{Store: st, Sync: func(ctx context.Context, s *Store) (SyncResult, error) {
		syncCalled = true
		n, _ := s.UnsyncedCount()
		return SyncResult{PostgresConfigured: false, Remaining: n}, nil
	}}
	if err := h.Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	resp := readAll(t, out.Bytes())
	if len(resp) != 8 {
		t.Fatalf("want 8 responses, got %d: %v", len(resp), resp)
	}
	if resp[0]["ok"] != true || resp[0]["version"] != Version || resp[0]["id"] != float64(1) {
		t.Errorf("ping: %v", resp[0])
	}
	targets := resp[1]["targets"].([]any)
	if resp[1]["intervalSeconds"] != float64(45) || len(targets) != 1 {
		t.Errorf("config: %v", resp[1])
	}
	if resp[2]["changed"] != true || resp[2]["status"] != StatusOK {
		t.Errorf("save: %v", resp[2])
	}
	if resp[3]["changed"] != false || resp[3]["snapshotID"] != resp[2]["snapshotID"] {
		t.Errorf("duplicate save: %v", resp[3])
	}
	if resp[4]["ok"] != false {
		t.Errorf("save without capture: %v", resp[4])
	}
	if !syncCalled || resp[5]["ok"] != true || resp[5]["remaining"] != float64(1) {
		t.Errorf("sync: %v", resp[5])
	}
	if resp[6]["ok"] != false || resp[6]["id"] != float64(7) {
		t.Errorf("unknown type: %v", resp[6])
	}
	if resp[7]["ok"] != false {
		t.Errorf("bad json: %v", resp[7])
	}
}

func TestHostSyncFailureKeepsData(t *testing.T) {
	st, _ := openTestStore(t)
	tg, _ := st.AddTarget("https://a.com", "")
	st.SaveCapture(okCapture(tg.ID, tg.URL, "T", "x", ""))
	var in, out bytes.Buffer
	in.Write(frame(t, map[string]any{"id": "s", "type": "sync"}))
	h := &Host{Store: st, Sync: func(ctx context.Context, s *Store) (SyncResult, error) {
		n, _ := s.UnsyncedCount()
		return SyncResult{PostgresConfigured: true, Remaining: n}, errors.New("connection refused")
	}}
	if err := h.Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	resp := readAll(t, out.Bytes())[0]
	if resp["ok"] != false || resp["remaining"] != float64(1) || !strings.Contains(resp["error"].(string), "refused") {
		t.Fatalf("sync failure response: %v", resp)
	}
	if n, _ := st.UnsyncedCount(); n != 1 {
		t.Fatalf("data must stay unsynced, got %d", n)
	}
}
