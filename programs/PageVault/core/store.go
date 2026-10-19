package core

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Store는 로컬 SQLite 저장소다. 확장 호스트와 트레이 앱이 동시에 열 수 있도록 WAL + busy_timeout을 쓴다.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

const schema = `
CREATE TABLE IF NOT EXISTS targets (
	id         INTEGER PRIMARY KEY,
	url        TEXT NOT NULL UNIQUE,
	name       TEXT NOT NULL DEFAULT '',
	enabled    INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS snapshots (
	id           INTEGER PRIMARY KEY,
	uid          TEXT NOT NULL UNIQUE,
	target_id    INTEGER REFERENCES targets(id) ON DELETE SET NULL,
	target_url   TEXT NOT NULL,
	target_name  TEXT NOT NULL DEFAULT '',
	final_url    TEXT NOT NULL DEFAULT '',
	title        TEXT NOT NULL DEFAULT '',
	text         TEXT,
	html         TEXT,
	content_hash TEXT NOT NULL,
	status       TEXT NOT NULL,
	error        TEXT NOT NULL DEFAULT '',
	captured_at  TEXT NOT NULL,
	last_seen_at TEXT NOT NULL,
	synced_at    TEXT
);
CREATE INDEX IF NOT EXISTS snapshots_target_url_id ON snapshots(target_url, id);
CREATE INDEX IF NOT EXISTS snapshots_unsynced ON snapshots(id) WHERE synced_at IS NULL;
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

func (s *Store) AddTarget(rawURL, name string) (Target, error) {
	u, err := ValidateTargetURL(rawURL)
	if err != nil {
		return Target{}, err
	}
	name = strings.TrimSpace(name)
	res, err := s.db.Exec(`INSERT INTO targets(url, name, enabled, created_at) VALUES(?, ?, 1, ?)`, u, name, s.nowString())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Target{}, errorf("이미 등록된 URL입니다: %s", u)
		}
		return Target{}, err
	}
	id, _ := res.LastInsertId()
	return s.Target(id)
}

func (s *Store) Target(id int64) (Target, error) {
	var t Target
	var enabled int
	err := s.db.QueryRow(`SELECT id, url, name, enabled, created_at FROM targets WHERE id = ?`, id).
		Scan(&t.ID, &t.URL, &t.Name, &enabled, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Target{}, errorf("대상 %d이(가) 없습니다", id)
	}
	t.Enabled = enabled != 0
	return t, err
}

func (s *Store) Targets(enabledOnly bool) ([]Target, error) {
	q := `SELECT id, url, name, enabled, created_at FROM targets`
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
		var t Target
		var enabled int
		if err := rows.Scan(&t.ID, &t.URL, &t.Name, &enabled, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.Enabled = enabled != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) RemoveTarget(id int64) error {
	return s.mustAffect(s.db.Exec(`DELETE FROM targets WHERE id = ?`, id))(id)
}

func (s *Store) SetTargetEnabled(id int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	return s.mustAffect(s.db.Exec(`UPDATE targets SET enabled = ? WHERE id = ?`, v, id))(id)
}

func (s *Store) RenameTarget(id int64, name string) error {
	return s.mustAffect(s.db.Exec(`UPDATE targets SET name = ? WHERE id = ?`, strings.TrimSpace(name), id))(id)
}

func (s *Store) mustAffect(res sql.Result, err error) func(int64) error {
	return func(id int64) error {
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errorf("대상 %d이(가) 없습니다", id)
		}
		return nil
	}
}

// TargetStates는 대상별 마지막 스냅샷 상태를 돌려준다.
func (s *Store) TargetStates() ([]TargetState, error) {
	rows, err := s.db.Query(`
		SELECT t.id, t.url, t.name, t.enabled, t.created_at,
		       COALESCE(s.status, ''), COALESCE(s.captured_at, ''), COALESCE(s.last_seen_at, '')
		FROM targets t
		LEFT JOIN snapshots s ON s.id = (SELECT MAX(id) FROM snapshots WHERE target_url = t.url)
		ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TargetState{}
	for rows.Next() {
		var st TargetState
		var enabled int
		if err := rows.Scan(&st.ID, &st.URL, &st.Name, &enabled, &st.CreatedAt,
			&st.LastStatus, &st.LastCapturedAt, &st.LastSeenAt); err != nil {
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

// ---- snapshots ----

// SaveResult는 save 요청의 응답 값이다.
type SaveResult struct {
	SnapshotID int64  `json:"snapshotID"`
	Status     string `json:"status"`
	Changed    bool   `json:"changed"`
}

const loginRequiredHash = "login_required"

// ClassifyCapture는 캡처의 상태와 content_hash를 정한다.
func ClassifyCapture(c Capture) (status, hash string) {
	switch {
	case strings.TrimSpace(c.Error) != "":
		return StatusError, sha256Hex("error\x1f" + c.Error)
	case IsLoginRedirect(c.RequestedURL, c.FinalURL):
		return StatusLoginRequired, loginRequiredHash
	default:
		// HTML은 매번 바뀌는 토큰이 섞여 있어 해시에 넣지 않는다.
		return StatusOK, sha256Hex(c.Title + "\x1f" + c.Text)
	}
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// SaveCapture는 캡처를 저장한다. 같은 target_url의 직전 스냅샷과 상태·해시가 같으면
// 새 행을 만들지 않고 last_seen_at만 갱신하고 다시 동기화 대상으로 만든다.
func (s *Store) SaveCapture(c Capture) (SaveResult, error) {
	targetURL := strings.TrimSpace(c.RequestedURL)
	var targetID sql.NullInt64
	targetName := ""
	if c.TargetID > 0 {
		if t, err := s.Target(c.TargetID); err == nil {
			targetID = sql.NullInt64{Int64: t.ID, Valid: true}
			targetName = t.Name
			if targetURL == "" {
				targetURL = t.URL
			}
		}
	}
	if targetURL == "" {
		return SaveResult{}, errorf("requestedURL이 비어 있습니다")
	}
	status, hash := ClassifyCapture(c)
	now := s.nowString()

	tx, err := s.db.Begin()
	if err != nil {
		return SaveResult{}, err
	}
	defer tx.Rollback()

	var lastID int64
	var lastStatus, lastHash string
	err = tx.QueryRow(`SELECT id, status, content_hash FROM snapshots WHERE target_url = ? ORDER BY id DESC LIMIT 1`, targetURL).
		Scan(&lastID, &lastStatus, &lastHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SaveResult{}, err
	}
	if err == nil && lastStatus == status && lastHash == hash {
		if _, err := tx.Exec(`UPDATE snapshots SET last_seen_at = ?, synced_at = NULL WHERE id = ?`, now, lastID); err != nil {
			return SaveResult{}, err
		}
		return SaveResult{SnapshotID: lastID, Status: status, Changed: false}, tx.Commit()
	}

	var text, html sql.NullString
	if status == StatusOK {
		text = sql.NullString{String: c.Text, Valid: true}
		html = sql.NullString{String: c.HTML, Valid: true}
	}
	errMsg := ""
	if status == StatusError {
		errMsg = c.Error
	}
	res, err := tx.Exec(`INSERT INTO snapshots(uid, target_id, target_url, target_name, final_url, title, text, html,
			content_hash, status, error, captured_at, last_seen_at, synced_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		uuid.NewString(), targetID, targetURL, targetName, c.FinalURL, c.Title, text, html,
		hash, status, errMsg, now, now)
	if err != nil {
		return SaveResult{}, err
	}
	id, _ := res.LastInsertId()
	return SaveResult{SnapshotID: id, Status: status, Changed: true}, tx.Commit()
}

const snapshotCols = `id, uid, target_id, target_url, target_name, final_url, title,
	COALESCE(text, ''), COALESCE(html, ''), content_hash, status, error, captured_at, last_seen_at, COALESCE(synced_at, '')`

func scanSnapshot(sc interface{ Scan(...any) error }) (Snapshot, error) {
	var sn Snapshot
	var tid sql.NullInt64
	err := sc.Scan(&sn.ID, &sn.UID, &tid, &sn.TargetURL, &sn.TargetName, &sn.FinalURL, &sn.Title,
		&sn.Text, &sn.HTML, &sn.ContentHash, &sn.Status, &sn.Error, &sn.CapturedAt, &sn.LastSeenAt, &sn.SyncedAt)
	if tid.Valid {
		sn.TargetID = &tid.Int64
	}
	return sn, err
}

// Snapshots는 최근 스냅샷 목록을 돌려준다 (본문·HTML 제외). url이 비어 있지 않으면 해당 대상만.
func (s *Store) Snapshots(limit int, targetURL string) ([]Snapshot, error) {
	if limit <= 0 {
		limit = 20
	}
	q := `SELECT ` + snapshotCols + ` FROM snapshots`
	args := []any{}
	if targetURL != "" {
		q += ` WHERE target_url = ?`
		args = append(args, targetURL)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Snapshot{}
	for rows.Next() {
		sn, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		sn.Text, sn.HTML = "", ""
		out = append(out, sn)
	}
	return out, rows.Err()
}

func (s *Store) Snapshot(id int64) (Snapshot, error) {
	sn, err := scanSnapshot(s.db.QueryRow(`SELECT `+snapshotCols+` FROM snapshots WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return sn, errorf("스냅샷 %d이(가) 없습니다", id)
	}
	return sn, err
}

// UnsyncedAfter는 id가 afterID보다 큰 미동기화 스냅샷을 limit개까지 돌려준다.
func (s *Store) UnsyncedAfter(afterID int64, limit int) ([]Snapshot, error) {
	rows, err := s.db.Query(`SELECT `+snapshotCols+` FROM snapshots
		WHERE synced_at IS NULL AND id > ? ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Snapshot{}
	for rows.Next() {
		sn, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

// MarkSynced는 보낸 시점의 last_seen_at이 그대로인 행만 동기화 완료로 표시한다.
// 보내는 사이에 갱신된 행은 synced_at이 NULL로 남아 다음에 다시 보낸다.
func (s *Store) MarkSynced(sent []Snapshot) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.nowString()
	for _, sn := range sent {
		if _, err := tx.Exec(`UPDATE snapshots SET synced_at = ? WHERE id = ? AND last_seen_at = ?`,
			now, sn.ID, sn.LastSeenAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UnsyncedCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM snapshots WHERE synced_at IS NULL`).Scan(&n)
	return n, err
}

// Status는 CLI·트레이에 보여 줄 요약이다.
func (s *Store) Status() (Status, error) {
	st := Status{Home: Home()}
	var err error
	if st.Settings, err = s.Settings(); err != nil {
		return st, err
	}
	if err = s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(enabled), 0) FROM targets`).Scan(&st.Targets, &st.EnabledCount); err != nil {
		return st, err
	}
	if err = s.db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(last_seen_at), '') FROM snapshots`).Scan(&st.Snapshots, &st.LastCapture); err != nil {
		return st, err
	}
	if st.Unsynced, err = s.UnsyncedCount(); err != nil {
		return st, err
	}
	states, err := s.TargetStates()
	if err != nil {
		return st, err
	}
	st.LoginRequired = []TargetState{}
	for _, t := range states {
		if t.Enabled && t.LastStatus == StatusLoginRequired {
			st.LoginRequired = append(st.LoginRequired, t)
		}
	}
	return st, nil
}
