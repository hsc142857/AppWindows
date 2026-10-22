package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func errorsAs(err error, target any) bool { return errors.As(err, target) }

func roundTrip(t *testing.T, h *Host, reqs ...any) []map[string]any {
	t.Helper()
	var in bytes.Buffer
	for _, r := range reqs {
		if err := WriteMessage(&in, r); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := h.Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	var resps []map[string]any
	for out.Len() > 0 {
		msg, err := ReadMessage(&out)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		json.Unmarshal(msg, &m)
		resps = append(resps, m)
	}
	return resps
}

func TestHostConfigSaveSync(t *testing.T) {
	st, _ := openTestStore(t)
	tg, _ := st.AddTarget("https://app.example.com/logs", "업무앱", "#activity table")
	syncCalled := false
	h := &Host{Store: st, Sync: func(ctx context.Context, st *Store) (SyncResult, error) {
		syncCalled = true
		n, _ := st.UnsyncedCount()
		return SyncResult{Remaining: n}, nil
	}}
	resps := roundTrip(t, h,
		map[string]any{"id": 1, "type": "config"},
		map[string]any{"id": 2, "type": "save", "capture": logCapture(tg.ID, []string{"09:00", "로그인"})},
		map[string]any{"id": 3, "type": "sync"},
		map[string]any{"id": 4, "type": "nope"},
	)
	if len(resps) != 4 {
		t.Fatalf("responses = %d", len(resps))
	}
	targets := resps[0]["targets"].([]any)
	if len(targets) != 1 || targets[0].(map[string]any)["selector"] != "#activity table" {
		t.Fatalf("config = %+v", resps[0])
	}
	if resps[1]["ok"] != true || resps[1]["newRows"].(float64) != 1 || resps[1]["status"] != StatusOK {
		t.Fatalf("save = %+v", resps[1])
	}
	if !syncCalled || resps[2]["remaining"].(float64) != 1 {
		t.Fatalf("sync = %+v", resps[2])
	}
	if resps[3]["ok"] != false {
		t.Fatalf("unknown type should fail: %+v", resps[3])
	}
}
