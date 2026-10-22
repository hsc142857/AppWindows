package core

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"appwindows/shared/apiclient"
	_ "modernc.org/sqlite"
)

// Store는 로컬 SQLite 저장소다. 확장 호스트와 CLI가 동시에 열 수 있도록 WAL + busy_timeout을 쓴다.
// 서버가 없거나 끊겨도 수집한 로그는 여기에 먼저 남고, 업로드는 나중에 다시 시도한다.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

const schema = `
CREATE TABLE IF NOT EXISTS targets (
	id         INTEGER PRIMARY KEY,
	url        TEXT NOT NULL UNIQUE,
	name       TEXT NOT NULL DEFAULT '',
	selector   TEXT NOT NULL DEFAULT '',
	enabled    INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS captures (
	id          INTEGER PRIMARY KEY,
	target_id   INTEGER REFERENCES targets(id) ON DELETE SET NULL,
	target_url  TEXT NOT NULL,
	target_name TEXT NOT NULL DEFAULT '',
	final_url   TEXT NOT NULL DEFAULT '',
	title       TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL,
	error       TEXT NOT NULL DEFAULT '',
	row_count   INTEGER NOT NULL DEFAULT 0,
	new_rows    INTEGER NOT NULL DEFAULT 0,
	captured_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS captures_target_url_id ON captures(target_url, id);
CREATE TABLE IF NOT EXISTS entries (
	id               INTEGER PRIMARY KEY,
	uid              TEXT NOT NULL UNIQUE,
	target_url       TEXT NOT NULL,
	target_name      TEXT NOT NULL DEFAULT '',
	headers          TEXT NOT NULL DEFAULT '[]',
	cells            TEXT NOT NULL,
	row_hash         TEXT NOT NULL,
	occurrence       INTEGER NOT NULL DEFAULT 1,
	first_capture_id INTEGER REFERENCES captures(id) ON DELETE SET NULL,
	first_seen_at    TEXT NOT NULL,
	last_seen_at     TEXT NOT NULL,
	synced_at        TEXT,
	sync_error       TEXT,
	UNIQUE(target_url, row_hash, occurrence)
);
CREATE INDEX IF NOT EXISTS entries_target_url_id ON entries(target_url, id);
CREATE INDEX IF NOT EXISTS entries_unsynced ON entries(id) WHERE synced_at IS NULL;
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

// OpenStore는 기본 위치(DBPath)의 저장소를 연다.
func OpenStore() (*Store, error) {
	if err := EnsureHome(); err != nil {
		return nil, err
	}
	return OpenStoreAt(DBPath())
}

func OpenStoreAt(path string) (*Store, error) {
	dsn := "file:" + filepath2URI(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// 한 프로세스 안에서는 연결 하나로 직렬화해 SQLITE_BUSY를 피한다.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, errorf("SQLite 스키마 생성 실패: %w", err)
	}
	return &Store{db: db, now: time.Now}, nil
}

// filepath2URI는 Windows 경로(C:\...)를 SQLite URI 경로로 바꾼다.
func filepath2URI(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if len(p) >= 2 && p[1] == ':' {
		p = "/" + p
	}
	return (&url.URL{Path: p}).EscapedPath()
}

func (s *Store) Close() error { return s.db.Close() }

// SetClock은 테스트에서 현재 시각을 바꿀 때 쓴다.
func (s *Store) SetClock(now func() time.Time) { s.now = now }

func (s *Store) nowString() string { return FormatTime(s.now()) }

// ---- targets ----

func (s *Store) AddTarget(rawURL, name, selector string) (Target, error) {
	u, err := ValidateTargetURL(rawURL)
	if err != nil {
		return Target{}, err
	}
	res, err := s.db.Exec(`INSERT INTO targets(url, name, selector, enabled, created_at) VALUES(?, ?, ?, 1, ?)`,
		u, strings.TrimSpace(name), strings.TrimSpace(selector), s.nowString())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Target{}, errorf("이미 등록된 URL입니다: %s", u)
		}
		return Target{}, err
	}
	id, _ := res.LastInsertId()
	return s.Target(id)
}

const targetCols = `id, url, name, selector, enabled, created_at`

func scanTarget(sc interface{ Scan(...any) error }) (Target, error) {
	var t Target
	var enabled int
	err := sc.Scan(&t.ID, &t.URL, &t.Name, &t.Selector, &enabled, &t.CreatedAt)
	t.Enabled = enabled != 0
	return t, err
}

func (s *Store) Target(id int64) (Target, error) {
	t, err := scanTarget(s.db.QueryRow(`SELECT `+targetCols+` FROM targets WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Target{}, errorf("대상 %d이(가) 없습니다", id)
	}
	return t, err
}

func (s *Store) Targets(enabledOnly bool) ([]Target, error) {
	q := `SELECT ` + targetCols + ` FROM targets`
	if enabledOnly {
		q += ` WHERE enabled = 1`
	}
	rows, err := s.db.Query(q + ` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Target{}
	for rows.Next() {
		t, err := scanTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RemoveTarget은 대상만 지운다. 이미 모은 로그(entries·captures)는 증빙이므로 남긴다.
func (s *Store) RemoveTarget(id int64) error {
	return mustAffect(id)(s.db.Exec(`DELETE FROM targets WHERE id = ?`, id))
}

func (s *Store) SetTargetEnabled(id int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	return mustAffect(id)(s.db.Exec(`UPDATE targets SET enabled = ? WHERE id = ?`, v, id))
}

func (s *Store) RenameTarget(id int64, name string) error {
	return mustAffect(id)(s.db.Exec(`UPDATE targets SET name = ? WHERE id = ?`, strings.TrimSpace(name), id))
}

// SetTargetSelector는 표를 찾을 CSS 선택자를 바꾼다. 빈 값이면 자동(행이 가장 많은 표).
func (s *Store) SetTargetSelector(id int64, selector string) error {
	return mustAffect(id)(s.db.Exec(`UPDATE targets SET selector = ? WHERE id = ?`, strings.TrimSpace(selector), id))
}

func mustAffect(id int64) func(sql.Result, error) error {
	return func(res sql.Result, err error) error {
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errorf("대상 %d이(가) 없습니다", id)
		}
		return nil
	}
}

// TargetStates는 대상별 로그 건수와 마지막 수집 상태를 돌려준다.
func (s *Store) TargetStates() ([]TargetState, error) {
	rows, err := s.db.Query(`
		SELECT t.id, t.url, t.name, t.selector, t.enabled, t.created_at,
		       (SELECT COUNT(*) FROM entries e WHERE e.target_url = t.url),
		       COALESCE(c.status, ''), COALESCE(c.error, ''), COALESCE(c.captured_at, '')
		FROM targets t
		LEFT JOIN captures c ON c.id = (SELECT MAX(id) FROM captures WHERE target_url = t.url)
		ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TargetState{}
	for rows.Next() {
		var st TargetState
		var enabled int
		if err := rows.Scan(&st.ID, &st.URL, &st.Name, &st.Selector, &enabled, &st.CreatedAt,
			&st.Entries, &st.LastStatus, &st.LastError, &st.LastCapturedAt); err != nil {
			return nil, err
		}
		st.Enabled = enabled != 0
		out = append(out, st)
	}
	return out, rows.Err()
}

// ---- settings ----

func (s *Store) Settings() (Settings, error) {
	st := Settings{IntervalSeconds: DefaultIntervalSeconds, LoadDelaySeconds: DefaultLoadDelaySeconds}
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return st, err
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			continue
		}
		switch k {
		case "interval_seconds":
			if ValidateInterval(n) == nil {
				st.IntervalSeconds = n
			}
		case "load_delay_seconds":
			if ValidateLoadDelay(n) == nil {
				st.LoadDelaySeconds = n
			}
		}
	}
	return st, rows.Err()
}

func (s *Store) SetInterval(sec int) error {
	if err := ValidateInterval(sec); err != nil {
		return err
	}
	return s.setSetting("interval_seconds", sec)
}

func (s *Store) SetLoadDelay(sec int) error {
	if err := ValidateLoadDelay(sec); err != nil {
		return err
	}
	return s.setSetting("load_delay_seconds", sec)
}

func (s *Store) setSetting(key string, v int) error {
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, strconv.Itoa(v))
	return err
}

// ---- captures / entries ----

// SaveResult는 save 요청의 응답 값이다.
type SaveResult struct {
	CaptureID int64  `json:"captureID"`
	Status    string `json:"status"`
	RowCount  int    `json:"rowCount"`
	NewRows   int    `json:"newRows"`
}

// ClassifyCapture는 수집 결과의 상태를 정한다.
func ClassifyCapture(c Capture) string {
	switch {
	case strings.TrimSpace(c.Error) != "":
		return StatusError
	case IsLoginRedirect(c.RequestedURL, c.FinalURL):
		return StatusLoginRequired
	}
	for _, r := range c.Rows {
		if normalizeRow(r) != nil {
			return StatusOK
		}
	}
	return StatusEmpty
}

// RowHash는 정리한 셀 내용의 해시다. 머리글은 넣지 않는다 (화면 개편으로 머리글만 바뀌어도 같은 행).
func RowHash(cells []string) string {
	h := sha256.Sum256([]byte(strings.Join(cells, "\x1f")))
	return hex.EncodeToString(h[:])
}

// EntryUID는 대상·행 내용·순번으로 정해지는 uid다. 같은 행은 다시 보내도(다른 PC에서 보내도) 같은 uid라
// 서버에서 중복되지 않는다.
func EntryUID(targetURL, rowHash string, occurrence int) string {
	h := sha256.Sum256([]byte(targetURL + "\x1f" + rowHash + "\x1f" + strconv.Itoa(occurrence)))
	return hex.EncodeToString(h[:16])
}

// SaveCapture는 수집 기록을 남기고, ok이면 처음 보는 행만 entries에 추가한다.
// 이미 있는 행은 last_seen_at만 갱신한다. 한 화면에 같은 내용의 행이 여러 개면 순번(occurrence)으로 구분한다.
func (s *Store) SaveCapture(c Capture) (SaveResult, error) {
	targetURL := strings.TrimSpace(c.RequestedURL)
	var targetID sql.NullInt64
	targetName := ""
	if c.TargetID > 0 {
		if t, err := s.Target(c.TargetID); err == nil {
			targetID = sql.NullInt64{Int64: t.ID, Valid: true}
			targetName = t.Name
			targetURL = t.URL // 확장이 보낸 값보다 등록된 URL을 기준으로 묶는다
		}
	}
	if targetURL == "" {
		return SaveResult{}, errorf("requestedURL이 비어 있습니다")
	}
	if len(c.Rows) > MaxRows {
		return SaveResult{}, errorf("행이 너무 많습니다: %d행 (최대 %d)", len(c.Rows), MaxRows)
	}
	status := ClassifyCapture(c)
	now := s.nowString()

	headers := make([]string, len(c.Headers))
	for i, h := range c.Headers {
		headers[i] = NormalizeCell(h)
	}
	headersJSON, _ := json.Marshal(headers)

	tx, err := s.db.Begin()
	if err != nil {
		return SaveResult{}, err
	}
	defer tx.Rollback()

	errMsg := ""
	if status == StatusError {
		errMsg = c.Error
	}
	res, err := tx.Exec(`INSERT INTO captures(target_id, target_url, target_name, final_url, title, status, error, captured_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, targetID, targetURL, targetName, c.FinalURL, c.Title, status, errMsg, now)
	if err != nil {
		return SaveResult{}, err
	}
	captureID, _ := res.LastInsertId()
	out := SaveResult{CaptureID: captureID, Status: status}

	if status == StatusOK {
		seen := map[string]int{}
		for _, raw := range c.Rows {
			cells := normalizeRow(raw)
			if cells == nil {
				continue
			}
			out.RowCount++
			hash := RowHash(cells)
			seen[hash]++
			occ := seen[hash]
			cellsJSON, _ := json.Marshal(cells)
			r, err := tx.Exec(`INSERT OR IGNORE INTO entries(uid, target_url, target_name, headers, cells, row_hash, occurrence,
					first_capture_id, first_seen_at, last_seen_at)
				VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				EntryUID(targetURL, hash, occ), targetURL, targetName, string(headersJSON), string(cellsJSON), hash, occ,
				captureID, now, now)
			if err != nil {
				return SaveResult{}, err
			}
			if n, _ := r.RowsAffected(); n == 1 {
				out.NewRows++
				continue
			}
			if _, err := tx.Exec(`UPDATE entries SET last_seen_at = ? WHERE target_url = ? AND row_hash = ? AND occurrence = ?`,
				now, targetURL, hash, occ); err != nil {
				return SaveResult{}, err
			}
		}
	}
	if _, err := tx.Exec(`UPDATE captures SET row_count = ?, new_rows = ? WHERE id = ?`, out.RowCount, out.NewRows, captureID); err != nil {
		return SaveResult{}, err
	}
	return out, tx.Commit()
}

// Captures는 최근 수집 기록이다. targetURL이 비어 있지 않으면 해당 대상만.
func (s *Store) Captures(limit int, targetURL string) ([]CaptureRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	q := `SELECT id, target_url, target_name, final_url, title, status, error, row_count, new_rows, captured_at FROM captures`
	args := []any{}
	if targetURL != "" {
		q += ` WHERE target_url = ?`
		args = append(args, targetURL)
	}
	rows, err := s.db.Query(q+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CaptureRecord{}
	for rows.Next() {
		var c CaptureRecord
		if err := rows.Scan(&c.ID, &c.TargetURL, &c.TargetName, &c.FinalURL, &c.Title, &c.Status, &c.Error,
			&c.RowCount, &c.NewRows, &c.CapturedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

const entryCols = `id, uid, target_url, target_name, headers, cells, row_hash, occurrence,
	first_seen_at, last_seen_at, COALESCE(synced_at, ''), COALESCE(sync_error, '')`

func scanEntry(sc interface{ Scan(...any) error }) (Entry, error) {
	var e Entry
	var headers, cells string
	if err := sc.Scan(&e.ID, &e.UID, &e.TargetURL, &e.TargetName, &headers, &cells, &e.RowHash, &e.Occurrence,
		&e.FirstSeenAt, &e.LastSeenAt, &e.SyncedAt, &e.SyncError); err != nil {
		return e, err
	}
	if err := json.Unmarshal([]byte(headers), &e.Headers); err != nil {
		return e, errorf("로그 #%d 머리글 형식 오류: %w", e.ID, err)
	}
	if err := json.Unmarshal([]byte(cells), &e.Cells); err != nil {
		return e, errorf("로그 #%d 내용 형식 오류: %w", e.ID, err)
	}
	return e, nil
}

func (s *Store) queryEntries(q string, args ...any) ([]Entry, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EntryFilter는 로그 조회·내보내기 조건이다. 비어 있는 값은 조건에서 뺀다.
// From·To는 처음 확인 시각(UTC 문자열) 기준이며 To는 포함하지 않는다.
type EntryFilter struct {
	TargetURL string
	From, To  string
	Limit     int  // 0이면 제한 없음
	Newest    bool // true면 최근 것부터
}

// Entries는 조건에 맞는 로그를 돌려준다. 기본은 저장 순서(오래된 것부터)다.
func (s *Store) Entries(f EntryFilter) ([]Entry, error) {
	q := `SELECT ` + entryCols + ` FROM entries WHERE 1 = 1`
	args := []any{}
	if f.TargetURL != "" {
		q += ` AND target_url = ?`
		args = append(args, f.TargetURL)
	}
	if f.From != "" {
		q += ` AND first_seen_at >= ?`
		args = append(args, f.From)
	}
	if f.To != "" {
		q += ` AND first_seen_at < ?`
		args = append(args, f.To)
	}
	if f.Newest {
		q += ` ORDER BY id DESC`
	} else {
		q += ` ORDER BY id`
	}
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	return s.queryEntries(q, args...)
}

// UnsyncedAfter는 id가 afterID보다 큰 미전송 로그를 limit개까지 돌려준다 (서버가 거부한 행 제외).
func (s *Store) UnsyncedAfter(afterID int64, limit int) ([]Entry, error) {
	return s.queryEntries(`SELECT `+entryCols+` FROM entries
		WHERE synced_at IS NULL AND sync_error IS NULL AND id > ? ORDER BY id LIMIT ?`, afterID, limit)
}

// MarkSynced는 서버가 받은 로그를 전송 완료로 표시한다.
func (s *Store) MarkSynced(sent []Entry) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.nowString()
	for _, e := range sent {
		if _, err := tx.Exec(`UPDATE entries SET synced_at = ? WHERE id = ?`, now, e.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MarkRejected는 서버가 거부한 행에 사유를 남긴다. 다시 보내도 같은 결과이므로 업로드에서 뺀다.
func (s *Store) MarkRejected(rejected []apiclient.Rejected) error {
	if len(rejected) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range rejected {
		msg := r.Error
		if msg == "" {
			msg = "서버가 거부함"
		}
		if _, err := tx.Exec(`UPDATE entries SET sync_error = ? WHERE uid = ? AND synced_at IS NULL`, msg, r.UID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClearRejected는 거부 표시를 지워 다음 업로드에 다시 넣는다 (서버 쪽을 고친 뒤 쓴다).
func (s *Store) ClearRejected() (int, error) {
	res, err := s.db.Exec(`UPDATE entries SET sync_error = NULL WHERE sync_error IS NOT NULL AND synced_at IS NULL`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// UnsyncedCount는 업로드할 건수다 (거부된 행 제외).
func (s *Store) UnsyncedCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM entries WHERE synced_at IS NULL AND sync_error IS NULL`).Scan(&n)
	return n, err
}

func (s *Store) RejectedCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM entries WHERE sync_error IS NOT NULL AND synced_at IS NULL`).Scan(&n)
	return n, err
}

// Status는 CLI에 보여 줄 요약이다.
func (s *Store) Status() (Status, error) {
	st := Status{Home: Home()}
	var err error
	if st.Settings, err = s.Settings(); err != nil {
		return st, err
	}
	if err = s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(enabled), 0) FROM targets`).Scan(&st.Targets, &st.EnabledCount); err != nil {
		return st, err
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM entries`).Scan(&st.Entries); err != nil {
		return st, err
	}
	if err = s.db.QueryRow(`SELECT COALESCE(MAX(captured_at), '') FROM captures`).Scan(&st.LastCapture); err != nil {
		return st, err
	}
	if st.Unsynced, err = s.UnsyncedCount(); err != nil {
		return st, err
	}
	if st.Rejected, err = s.RejectedCount(); err != nil {
		return st, err
	}
	states, err := s.TargetStates()
	if err != nil {
		return st, err
	}
	st.NeedAttention = []TargetState{}
	for _, t := range states {
		if t.Enabled && t.LastStatus != "" && t.LastStatus != StatusOK {
			st.NeedAttention = append(st.NeedAttention, t)
		}
	}
	return st, nil
}
