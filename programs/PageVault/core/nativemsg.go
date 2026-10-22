package core

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log"
	"time"
)

// Chrome Native Messaging: 4바이트 길이(리틀 엔디언 uint32) + UTF-8 JSON.
const (
	MaxIncomingMessage = 64 << 20 // Chrome → 호스트
	MaxOutgoingMessage = 1 << 20  // 호스트 → Chrome
)

var ErrMessageTooLarge = errors.New("메시지가 허용 크기를 넘었습니다")

// ReadMessage는 메시지 하나를 읽는다. 스트림이 메시지 경계에서 끝나면 io.EOF를 돌려준다.
func ReadMessage(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, errorf("메시지 길이 헤더가 잘렸습니다: %w", err)
		}
		return nil, err
	}
	n := binary.LittleEndian.Uint32(hdr[:])
	if n > MaxIncomingMessage {
		return nil, errorf("%w (%d바이트)", ErrMessageTooLarge, n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, errorf("메시지 본문이 잘렸습니다: %w", err)
	}
	return buf, nil
}

// WriteMessage는 v를 JSON으로 인코딩해 메시지 하나로 쓴다.
func WriteMessage(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(body) > MaxOutgoingMessage {
		return errorf("%w (%d바이트)", ErrMessageTooLarge, len(body))
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(body)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

type Request struct {
	ID      json.RawMessage `json:"id"`
	Type    string          `json:"type"`
	Capture *Capture        `json:"capture,omitempty"`
}

// SyncFunc는 sync 요청을 처리한다. 테스트에서 바꿔 끼울 수 있게 함수로 둔다.
type SyncFunc func(ctx context.Context, st *Store) (SyncResult, error)

type Host struct {
	Store *Store
	Log   *log.Logger
	Sync  SyncFunc
}

// Serve는 r에서 요청을 읽어 w로 응답한다. Chrome이 포트를 닫으면(EOF) nil로 끝난다.
// w(stdout)는 통신 전용이므로 로그는 h.Log에만 쓴다.
func (h *Host) Serve(r io.Reader, w io.Writer) error {
	br := bufio.NewReader(r)
	for {
		msg, err := ReadMessage(br)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			h.logf("읽기 실패: %v", err)
			return err
		}
		resp := h.handle(msg)
		if err := WriteMessage(w, resp); err != nil {
			h.logf("쓰기 실패: %v", err)
			// 응답이 너무 크면 오류 응답이라도 보낸다.
			if errors.Is(err, ErrMessageTooLarge) {
				_ = WriteMessage(w, map[string]any{"id": resp["id"], "ok": false, "error": err.Error()})
				continue
			}
			return err
		}
	}
}

func (h *Host) handle(msg []byte) map[string]any {
	var req Request
	if err := json.Unmarshal(msg, &req); err != nil {
		h.logf("잘못된 요청: %v", err)
		return map[string]any{"id": nil, "ok": false, "error": "잘못된 JSON 요청: " + err.Error()}
	}
	resp := map[string]any{"id": req.ID, "ok": true}
	fail := func(err error) map[string]any {
		h.logf("%s 실패: %v", req.Type, err)
		resp["ok"] = false
		resp["error"] = err.Error()
		return resp
	}
	switch req.Type {
	case "ping":
		resp["version"] = Version
	case "config":
		st, err := h.Store.Settings()
		if err != nil {
			return fail(err)
		}
		targets, err := h.Store.Targets(true)
		if err != nil {
			return fail(err)
		}
		list := make([]map[string]any, 0, len(targets))
		for _, t := range targets {
			list = append(list, map[string]any{"id": t.ID, "url": t.URL, "name": t.Name})
		}
		resp["intervalSeconds"] = st.IntervalSeconds
		resp["loadDelaySeconds"] = st.LoadDelaySeconds
		resp["targets"] = list
	case "save":
		if req.Capture == nil {
			return fail(errorf("capture가 없습니다"))
		}
		res, err := h.Store.SaveCapture(*req.Capture)
		if err != nil {
			return fail(err)
		}
		h.logf("save %s → #%d %s changed=%v", req.Capture.RequestedURL, res.SnapshotID, res.Status, res.Changed)
		resp["snapshotID"] = res.SnapshotID
		resp["status"] = res.Status
		resp["changed"] = res.Changed
	case "sync":
		syncFn := h.Sync
		if syncFn == nil {
			syncFn = SyncConfigured
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := syncFn(ctx, h.Store)
		resp["apiConfigured"] = res.APIConfigured
		resp["sent"] = res.Sent
		resp["rejected"] = res.Rejected
		resp["remaining"] = res.Remaining
		if err != nil {
			// 데이터는 SQLite에 남아 있으므로 다음 라운드에 다시 보낸다.
			return fail(err)
		}
		h.logf("sync sent=%d rejected=%d remaining=%d", res.Sent, res.Rejected, res.Remaining)
	default:
		return fail(errorf("알 수 없는 요청 type: %q", req.Type))
	}
	return resp
}

func (h *Host) logf(format string, a ...any) {
	if h.Log != nil {
		h.Log.Printf(format, a...)
	}
}
