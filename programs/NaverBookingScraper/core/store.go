package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"appwindows/shared/apiclient"
	_ "modernc.org/sqlite"
)

// Store는 긁어 온 예약을 보관하는 로컬 SQLite(아웃박스)다.
// 예약은 먼저 여기에 저장되고, 서버가 처리했다고 응답한 버전만 "전송 완료"가 된다.
//
// 전송 상태 (예약마다 하나):
//
//	sent           sent_hash = content_hash — 지금 내용이 서버에 반영됨 (GUI "전송 완료")
//	pending        아직 안 보냈거나, 보낸 뒤 내용이 바뀜. last_error가 있으면 전송 실패 → 다음에 자동 재시도
//	rejected       서버(또는 사전 검증)가 이 내용을 거부 — 내용이 바뀌거나 "다시 전송"할 때까지 보내지 않음
//	other_partner  다른 업체 키로 긁어 온 예약 — 지금 키로는 보내지 않음
type Store struct {
	db  *sql.DB
	now func() time.Time
}

const (
	StateSent         = "sent"
	StatePending      = "pending"
	StateRejected     = "rejected"
	StateOtherPartner = "other_partner"
)

const storeSchema = `
CREATE TABLE IF NOT EXISTS reservations (
	partner_id    TEXT NOT NULL,
	booking_id    TEXT NOT NULL,
	data          TEXT NOT NULL,             -- Reservation JSON (마지막으로 긁어 온 내용)
	content_hash  TEXT NOT NULL,             -- 보낼 항목(ImportItem)의 SHA-256
	action        TEXT NOT NULL,             -- upsert | cancel
	start_at      TEXT NOT NULL DEFAULT '',
	first_seen_at TEXT NOT NULL,
	last_seen_at  TEXT NOT NULL,
	sent_hash     TEXT,                      -- 서버가 처리한 내용의 해시
	sent_at       TEXT,
	sent_result   TEXT,                      -- created · updated · skipped · cancelled
	reject_error  TEXT,                      -- 서버·사전 검증 거부 사유 (지금 내용 기준)
	last_error    TEXT,                      -- 마지막 전송 실패 (연결 오류 등, 자동 재시도)
	attempt_at    TEXT,
	PRIMARY KEY (partner_id, booking_id)
);
CREATE INDEX IF NOT EXISTS reservations_sent_at ON reservations(sent_at);
`

func OpenStore() (*Store, error) {
	if err := EnsureHome(); err != nil {
		return nil, err
	}
	return OpenStoreAt(DBPath())
}

func OpenStoreAt(path string) (*Store, error) {
	p := strings.ReplaceAll(path, `\`, "/")
	if len(p) >= 2 && p[1] == ':' {
		p = "/" + p // Windows 드라이브 경로
	}
	dsn := "file:" + (&url.URL{Path: p}).EscapedPath() +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(storeSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("SQLite 스키마 생성 실패: %w", err)
	}
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) Close() error                  { return s.db.Close() }
func (s *Store) SetClock(now func() time.Time) { s.now = now }
func (s *Store) nowString() string             { return s.now().UTC().Format(time.RFC3339Nano) }

// itemHash는 서버로 보낼 항목의 해시다. 보낼 내용이 같으면 다시 보내지 않는다.
func itemHash(it ImportItem) (string, error) {
	b, err := json.Marshal(it)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// SaveSummary는 긁어 온 예약을 저장한 결과다.
type SaveSummary struct {
	New       int `json:"new"`       // 처음 본 예약
	Changed   int `json:"changed"`   // 내용이 바뀐 예약 (다시 보내야 함)
	Unchanged int `json:"unchanged"` // 그대로인 예약
	Skipped   int `json:"skipped"`   // 예약번호가 없어 저장하지 않음
}

// SaveScraped는 긁어 온 예약을 partnerID(긁어 온 시점의 인증키 업체)로 저장한다.
// 내용(보낼 항목)이 바뀐 예약만 다시 전송 대상이 된다.
func (s *Store) SaveScraped(partnerID string, list []Reservation) (SaveSummary, error) {
	var sum SaveSummary
	if partnerID == "" {
		return sum, errors.New("업체가 확인되지 않았습니다 — 먼저 서버에 연결해 인증키의 업체를 확인하세요")
	}
	now := s.nowString()
	tx, err := s.db.Begin()
	if err != nil {
		return sum, err
	}
	defer tx.Rollback()
	for _, r := range list {
		r.BookingID = strings.TrimSpace(r.BookingID)
		if r.BookingID == "" {
			sum.Skipped++
			continue
		}
		if r.CapturedAt == "" {
			r.CapturedAt = now
		}
		it := r.ToImportItem()
		hash, err := itemHash(it)
		if err != nil {
			return sum, err
		}
		data, _ := json.Marshal(r)
		var oldHash string
		err = tx.QueryRow(`SELECT content_hash FROM reservations WHERE partner_id = ? AND booking_id = ?`,
			partnerID, r.BookingID).Scan(&oldHash)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			sum.New++
			_, err = tx.Exec(`INSERT INTO reservations(partner_id, booking_id, data, content_hash, action, start_at,
				first_seen_at, last_seen_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
				partnerID, r.BookingID, string(data), hash, it.Action, r.StartAt, now, now)
		case err != nil:
			return sum, err
		case oldHash == hash:
			sum.Unchanged++
			_, err = tx.Exec(`UPDATE reservations SET data = ?, last_seen_at = ? WHERE partner_id = ? AND booking_id = ?`,
				string(data), now, partnerID, r.BookingID)
		default:
			// 내용이 바뀌면 이전 거부 사유는 지금 내용과 상관없으므로 지운다.
			sum.Changed++
			_, err = tx.Exec(`UPDATE reservations SET data = ?, content_hash = ?, action = ?, start_at = ?, last_seen_at = ?,
				reject_error = NULL WHERE partner_id = ? AND booking_id = ?`,
				string(data), hash, it.Action, r.StartAt, now, partnerID, r.BookingID)
		}
		if err != nil {
			return sum, err
		}
	}
	return sum, tx.Commit()
}

// UploadSummary는 전송 결과다.
type UploadSummary struct {
	Sent      int    `json:"sent"`      // 이번에 서버가 처리한 건수 (전송 완료로 바뀜)
	Rejected  int    `json:"rejected"`  // 이번에 거부된 건수
	Remaining int    `json:"remaining"` // 아직 안 간 건수 (대기 + 전송 실패)
	Error     string `json:"error"`     // 전송 자체가 실패했을 때 (다음에 자동 재시도)
}

type outRow struct {
	bookingID string
	hash      string
	item      ImportItem
}

// UploadPending은 지금 업체(partnerID)의 미전송 예약(대기·전송 실패)을 보낸다.
// 서버가 처리했다고 응답한 건만, 보낸 내용 그대로일 때 전송 완료로 표시한다.
func (s *Store) UploadPending(ctx context.Context, c apiclient.Config, partnerID string) (sum UploadSummary, err error) {
	defer func() { sum.Remaining, _ = s.unsentCount(partnerID) }()
	rows, err := s.db.Query(`SELECT booking_id, data, content_hash FROM reservations
		WHERE partner_id = ? AND (sent_hash IS NULL OR sent_hash != content_hash) AND reject_error IS NULL
		ORDER BY start_at, booking_id`, partnerID)
	if err != nil {
		return sum, err
	}
	var out []outRow
	for rows.Next() {
		var id, data, hash string
		if err := rows.Scan(&id, &data, &hash); err != nil {
			rows.Close()
			return sum, err
		}
		var r Reservation
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			rows.Close()
			return sum, err
		}
		out = append(out, outRow{bookingID: id, hash: hash, item: r.ToImportItem()})
	}
	rows.Close()
	if len(out) == 0 {
		return sum, nil
	}
	items := make([]ImportItem, len(out))
	for i, o := range out {
		items[i] = o.item
	}
	res, sendErr := Import(ctx, c, items, false)
	now := s.nowString()

	byID := make(map[string]ImportItemResult, len(res.Results))
	for _, r := range res.Results {
		byID[r.ExternalBookingID] = r
	}
	tx, err := s.db.Begin()
	if err != nil {
		return sum, err
	}
	defer tx.Rollback()
	for _, o := range out {
		r, ok := byID[o.bookingID]
		switch {
		case !ok:
			// 응답이 없는 건 (전송 실패로 못 간 배치 등): 다음에 자동 재시도
			msg := "서버 응답 없음"
			if sendErr != nil {
				msg = sendErr.Error()
			}
			_, err = tx.Exec(`UPDATE reservations SET last_error = ?, attempt_at = ? WHERE partner_id = ? AND booking_id = ?`,
				msg, now, partnerID, o.bookingID)
		case r.Result == "error":
			sum.Rejected++
			_, err = tx.Exec(`UPDATE reservations SET reject_error = ?, last_error = NULL, attempt_at = ?
				WHERE partner_id = ? AND booking_id = ? AND content_hash = ?`,
				orText(r.Error, "서버가 거부함"), now, partnerID, o.bookingID, o.hash)
		default:
			// 보내는 사이 내용이 바뀌었으면(content_hash가 다르면) 완료로 표시하지 않는다 — 다음에 새 내용을 보낸다.
			var res sql.Result
			res, err = tx.Exec(`UPDATE reservations SET sent_hash = ?, sent_at = ?, sent_result = ?, last_error = NULL,
				reject_error = NULL, attempt_at = ? WHERE partner_id = ? AND booking_id = ? AND content_hash = ?`,
				o.hash, now, r.Result, now, partnerID, o.bookingID, o.hash)
			if err == nil {
				if n, _ := res.RowsAffected(); n > 0 {
					sum.Sent++
				}
			}
		}
		if err != nil {
			return sum, err
		}
	}
	if err := tx.Commit(); err != nil {
		return sum, err
	}
	if sendErr != nil {
		sum.Error = sendErr.Error()
	}
	return sum, nil
}

// RetryRejected는 거부된 예약을 다시 전송 대상으로 돌린다 (서버 쪽을 고친 뒤 쓴다).
func (s *Store) RetryRejected(partnerID string) (int, error) {
	res, err := s.db.Exec(`UPDATE reservations SET reject_error = NULL WHERE partner_id = ? AND reject_error IS NOT NULL`, partnerID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *Store) unsentCount(partnerID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM reservations WHERE partner_id = ?
		AND (sent_hash IS NULL OR sent_hash != content_hash) AND reject_error IS NULL`, partnerID).Scan(&n)
	return n, err
}

// LocalReservation은 GUI에 보여 줄 한 줄이다.
type LocalReservation struct {
	Reservation
	PartnerID   string `json:"partnerID"`
	State       string `json:"state"` // sent · pending · rejected · other_partner
	EverSent    bool   `json:"everSent"`
	SentAt      string `json:"sentAt"`
	SentResult  string `json:"sentResult"`
	RejectError string `json:"rejectError"`
	LastError   string `json:"lastError"`
	AttemptAt   string `json:"attemptAt"`
	FirstSeenAt string `json:"firstSeenAt"`
	LastSeenAt  string `json:"lastSeenAt"`
}

type Counts struct {
	Sent         int `json:"sent"`
	Pending      int `json:"pending"`  // 대기 + 전송 실패
	Failed       int `json:"failed"`   // 그중 전송 실패 (자동 재시도)
	Rejected     int `json:"rejected"` // 거부
	OtherPartner int `json:"otherPartner"`
}

// List는 저장된 예약을 상태와 함께 돌려준다. partnerID는 지금 인증키의 업체다.
func (s *Store) List(partnerID string) ([]LocalReservation, Counts, error) {
	var counts Counts
	rows, err := s.db.Query(`SELECT partner_id, data, content_hash, COALESCE(sent_hash, ''), COALESCE(sent_at, ''),
		COALESCE(sent_result, ''), COALESCE(reject_error, ''), COALESCE(last_error, ''), COALESCE(attempt_at, ''),
		first_seen_at, last_seen_at FROM reservations ORDER BY start_at DESC, booking_id`)
	if err != nil {
		return nil, counts, err
	}
	defer rows.Close()
	out := []LocalReservation{}
	for rows.Next() {
		var lr LocalReservation
		var data, hash, sentHash string
		if err := rows.Scan(&lr.PartnerID, &data, &hash, &sentHash, &lr.SentAt, &lr.SentResult, &lr.RejectError,
			&lr.LastError, &lr.AttemptAt, &lr.FirstSeenAt, &lr.LastSeenAt); err != nil {
			return nil, counts, err
		}
		if err := json.Unmarshal([]byte(data), &lr.Reservation); err != nil {
			return nil, counts, err
		}
		lr.EverSent = sentHash != ""
		switch {
		case lr.PartnerID != partnerID:
			lr.State = StateOtherPartner
			counts.OtherPartner++
		case sentHash == hash:
			lr.State = StateSent
			counts.Sent++
		case lr.RejectError != "":
			lr.State = StateRejected
			counts.Rejected++
		default:
			lr.State = StatePending
			counts.Pending++
			if lr.LastError != "" {
				counts.Failed++
			}
		}
		out = append(out, lr)
	}
	return out, counts, rows.Err()
}

func orText(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
