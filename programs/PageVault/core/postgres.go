package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	DefaultPGTable   = "pagevault_snapshots"
	pgConnectTimeout = 10 * time.Second
	pgBatchSize      = 100
)

// 테이블 이름은 SQL에 직접 들어가므로 영문·숫자·밑줄(선택적으로 schema.table)만 허용한다.
var tableNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)

func ValidateTableName(name string) error {
	if !tableNameRe.MatchString(name) || len(name) > 127 {
		return errorf("테이블 이름은 영문·숫자·밑줄(선택: schema.table)만 쓸 수 있습니다: %q", name)
	}
	return nil
}

// PGConfig는 원격 Postgres 접속 정보다. URL에는 비밀번호가 포함된다.
type PGConfig struct {
	URL   string
	Table string
}

// pgFile은 postgres.json의 형식이다. 비밀번호는 URL에서 떼어 따로 보호해 저장한다.
type pgFile struct {
	URL           string `json:"url"`
	Table         string `json:"table"`
	PasswordDPAPI string `json:"password_dpapi,omitempty"` // Windows: DPAPI(현재 사용자)로 암호화
	Password      string `json:"password,omitempty"`       // 그 밖의 OS: 파일 권한(0600)으로만 보호
}

// ParsePGURL은 postgres:// URL을 검증하고 정리해 돌려준다.
func ParsePGURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, errorf("Postgres URL을 해석할 수 없습니다: %w", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, errorf("Postgres URL은 postgres:// 로 시작해야 합니다")
	}
	if u.Hostname() == "" {
		return nil, errorf("Postgres URL에 호스트가 없습니다")
	}
	if u.User == nil || u.User.Username() == "" {
		return nil, errorf("Postgres URL에 사용자 이름이 없습니다")
	}
	if strings.Trim(u.Path, "/") == "" {
		return nil, errorf("Postgres URL에 데이터베이스 이름이 없습니다")
	}
	q := u.Query()
	switch q.Get("sslmode") {
	case "", "disable", "prefer", "require":
	default:
		return nil, errorf("sslmode는 disable, prefer, require 중 하나여야 합니다: %q", q.Get("sslmode"))
	}
	for k := range q {
		if k != "sslmode" {
			return nil, errorf("지원하지 않는 URL 옵션입니다: %s (sslmode만 지원)", k)
		}
	}
	return u, nil
}

func NewPGConfig(rawURL, table string) (PGConfig, error) {
	u, err := ParsePGURL(rawURL)
	if err != nil {
		return PGConfig{}, err
	}
	if table == "" {
		table = DefaultPGTable
	}
	if err := ValidateTableName(table); err != nil {
		return PGConfig{}, err
	}
	return PGConfig{URL: u.String(), Table: table}, nil
}

// Masked는 비밀번호를 ****로 가린 URL이다.
func (c PGConfig) Masked() string { return MaskPGURL(c.URL) }

func MaskPGURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(잘못된 URL)"
	}
	if u.User != nil {
		if _, ok := u.User.Password(); ok {
			// url 패키지는 비밀번호의 *를 %2A로 인코딩하므로 자리표시자를 넣고 바꾼다.
			u.User = url.UserPassword(u.User.Username(), "PVMASK")
			return strings.Replace(u.String(), ":PVMASK@", ":****@", 1)
		}
	}
	return u.String()
}

func LoadPGConfig() (PGConfig, bool, error) {
	data, err := os.ReadFile(PostgresPath())
	if errors.Is(err, os.ErrNotExist) {
		return PGConfig{}, false, nil
	}
	if err != nil {
		return PGConfig{}, false, err
	}
	var f pgFile
	if err := json.Unmarshal(data, &f); err != nil {
		return PGConfig{}, false, errorf("postgres.json을 읽을 수 없습니다: %w", err)
	}
	u, err := url.Parse(f.URL)
	if err != nil {
		return PGConfig{}, false, err
	}
	password := f.Password
	if f.PasswordDPAPI != "" {
		if password, err = unprotectSecret(f.PasswordDPAPI); err != nil {
			return PGConfig{}, false, errorf("저장된 비밀번호를 복호화할 수 없습니다 (다른 사용자·PC에서 저장했나요?): %w", err)
		}
	}
	if u.User != nil && password != "" {
		u.User = url.UserPassword(u.User.Username(), password)
	}
	table := f.Table
	if table == "" {
		table = DefaultPGTable
	}
	return PGConfig{URL: u.String(), Table: table}, true, nil
}

func SavePGConfig(c PGConfig) error {
	if err := EnsureHome(); err != nil {
		return err
	}
	u, err := url.Parse(c.URL)
	if err != nil {
		return err
	}
	f := pgFile{Table: c.Table}
	if u.User != nil {
		if pw, ok := u.User.Password(); ok && pw != "" {
			if secretProtectionAvailable {
				if f.PasswordDPAPI, err = protectSecret(pw); err != nil {
					return errorf("비밀번호 암호화 실패: %w", err)
				}
			} else {
				f.Password = pw
			}
			u.User = url.User(u.User.Username())
		}
	}
	f.URL = u.String()
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := PostgresPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, PostgresPath())
}

func ClearPGConfig() error {
	err := os.Remove(PostgresPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func connectPG(ctx context.Context, c PGConfig) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(c.URL)
	if err != nil {
		return nil, errorf("Postgres 설정 오류: %w", err)
	}
	cfg.ConnectTimeout = pgConnectTimeout
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, errorf("Postgres 접속 실패 (%s): %w", c.Masked(), err)
	}
	return conn, nil
}

func quoteTable(table string) string {
	return pgx.Identifier(strings.Split(table, ".")).Sanitize()
}

func indexName(table string) string {
	n := strings.ReplaceAll(table, ".", "_") + "_target_url_captured_at_idx"
	if len(n) > 63 {
		n = n[len(n)-63:]
	}
	return pgx.Identifier{n}.Sanitize()
}

func ensurePGTable(ctx context.Context, conn *pgx.Conn, table string) error {
	t := quoteTable(table)
	_, err := conn.Exec(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		uid          uuid PRIMARY KEY,
		target_url   text NOT NULL,
		target_name  text NOT NULL DEFAULT '',
		final_url    text NOT NULL DEFAULT '',
		title        text NOT NULL DEFAULT '',
		text         text,
		html         text,
		content_hash text NOT NULL,
		status       text NOT NULL,
		error        text NOT NULL DEFAULT '',
		captured_at  timestamptz NOT NULL,
		last_seen_at timestamptz NOT NULL,
		source_host  text NOT NULL DEFAULT '',
		synced_at    timestamptz NOT NULL DEFAULT now()
	)`, t))
	if err != nil {
		return errorf("Postgres 테이블 생성 실패: %w", err)
	}
	_, err = conn.Exec(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (target_url, captured_at)`, indexName(table), t))
	if err != nil {
		return errorf("Postgres 인덱스 생성 실패: %w", err)
	}
	return nil
}

// TestPG는 접속하고 테이블을 만들 수 있는지 확인한다. 서버 버전을 돌려준다.
func TestPG(ctx context.Context, c PGConfig) (string, error) {
	conn, err := connectPG(ctx, c)
	if err != nil {
		return "", err
	}
	defer conn.Close(context.Background())
	var version string
	if err := conn.QueryRow(ctx, `SHOW server_version`).Scan(&version); err != nil {
		return "", err
	}
	if err := ensurePGTable(ctx, conn, c.Table); err != nil {
		return version, err
	}
	return version, nil
}

type SyncResult struct {
	PostgresConfigured bool `json:"postgresConfigured"`
	Sent               int  `json:"sent"`
	Remaining          int  `json:"remaining"`
}

// SyncConfigured는 저장된 설정으로 동기화한다. 설정이 없으면 아무것도 보내지 않는다.
func SyncConfigured(ctx context.Context, st *Store) (SyncResult, error) {
	c, ok, err := LoadPGConfig()
	if err != nil || !ok {
		remaining, _ := st.UnsyncedCount()
		return SyncResult{Remaining: remaining}, err
	}
	return Sync(ctx, st, c)
}

// Sync는 미동기화 스냅샷을 100개씩 upsert하고, 보낸 시점과 last_seen_at이 같은 행만 완료 표시한다.
// 중간에 실패해도 보내지 못한 행은 SQLite에 synced_at NULL로 남는다.
func Sync(ctx context.Context, st *Store, c PGConfig) (res SyncResult, err error) {
	res.PostgresConfigured = true
	defer func() { res.Remaining, _ = st.UnsyncedCount() }()

	conn, err := connectPG(ctx, c)
	if err != nil {
		return res, err
	}
	defer conn.Close(context.Background())
	if err := ensurePGTable(ctx, conn, c.Table); err != nil {
		return res, err
	}
	sourceHost, _ := os.Hostname()
	insert := fmt.Sprintf(`INSERT INTO %s (uid, target_url, target_name, final_url, title, text, html,
			content_hash, status, error, captured_at, last_seen_at, source_host, synced_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now())
		ON CONFLICT (uid) DO UPDATE SET last_seen_at = EXCLUDED.last_seen_at, synced_at = now()`, quoteTable(c.Table))

	var afterID int64
	for {
		rows, err := st.UnsyncedAfter(afterID, pgBatchSize)
		if err != nil {
			return res, err
		}
		if len(rows) == 0 {
			break
		}
		if err := sendBatch(ctx, conn, insert, sourceHost, rows); err != nil {
			return res, errorf("Postgres 전송 실패: %w", err)
		}
		if err := st.MarkSynced(rows); err != nil {
			return res, err
		}
		res.Sent += len(rows)
		afterID = rows[len(rows)-1].ID
	}
	return res, nil
}

func sendBatch(ctx context.Context, conn *pgx.Conn, insert, sourceHost string, rows []Snapshot) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	b := &pgx.Batch{}
	for _, sn := range rows {
		captured, err := ParseTime(sn.CapturedAt)
		if err != nil {
			return errorf("스냅샷 %d captured_at 형식 오류: %w", sn.ID, err)
		}
		lastSeen, err := ParseTime(sn.LastSeenAt)
		if err != nil {
			return errorf("스냅샷 %d last_seen_at 형식 오류: %w", sn.ID, err)
		}
		var text, html *string
		if sn.Status == StatusOK {
			text, html = &sn.Text, &sn.HTML
		}
		b.Queue(insert, sn.UID, sn.TargetURL, sn.TargetName, sn.FinalURL, sn.Title, text, html,
			sn.ContentHash, sn.Status, sn.Error, captured, lastSeen, sourceHost)
	}
	if err := tx.SendBatch(ctx, b).Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
