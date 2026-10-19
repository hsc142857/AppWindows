package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParsePGURL(t *testing.T) {
	ok := []string{
		"postgres://u:p@db.example.com:5432/pv",
		"postgresql://u:p@db.example.com/pv?sslmode=require",
		"postgres://u@10.0.0.1:5432/pv?sslmode=disable",
	}
	for _, u := range ok {
		if _, err := ParsePGURL(u); err != nil {
			t.Errorf("%q: %v", u, err)
		}
	}
	bad := []string{
		"mysql://u:p@h/db",
		"postgres://u:p@/db",
		"postgres://h:5432/db",
		"postgres://u:p@h:5432",
		"postgres://u:p@h/db?sslmode=verify-full",
		"postgres://u:p@h/db?sslmode=require&host=/tmp",
	}
	for _, u := range bad {
		if _, err := ParsePGURL(u); err == nil {
			t.Errorf("%q: expected error", u)
		}
	}
}

func TestValidateTableName(t *testing.T) {
	for _, n := range []string{"pagevault_snapshots", "ops.pv_snap", "_t1"} {
		if err := ValidateTableName(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}
	for _, n := range []string{"", "1abc", "a;drop table x", "a.b.c", `a"b`, "a-b", "a b", "a."} {
		if err := ValidateTableName(n); err == nil {
			t.Errorf("%q: expected error", n)
		}
	}
	if got := quoteTable("ops.pv_snap"); got != `"ops"."pv_snap"` {
		t.Errorf("quoteTable = %s", got)
	}
}

func TestMaskPGURL(t *testing.T) {
	got := MaskPGURL("postgres://u:s%40cret@h:5432/db?sslmode=require")
	if got != "postgres://u:****@h:5432/db?sslmode=require" {
		t.Fatalf("masked = %s", got)
	}
	if got := MaskPGURL("postgres://u@h/db"); strings.Contains(got, "****") {
		t.Fatalf("no password should not be masked: %s", got)
	}
}

func TestPGConfigSaveLoad(t *testing.T) {
	t.Setenv("PAGEVAULT_HOME", t.TempDir())
	if _, ok, err := LoadPGConfig(); ok || err != nil {
		t.Fatalf("empty load: ok=%v err=%v", ok, err)
	}
	c, err := NewPGConfig("postgres://u:p%40ss%20w@h:5432/db?sslmode=prefer", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Table != DefaultPGTable {
		t.Fatalf("default table = %s", c.Table)
	}
	if err := SavePGConfig(c); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(PostgresPath())
	if strings.Contains(string(raw), "u:p") {
		t.Fatalf("password must not stay in the url field: %s", raw)
	}
	if fi, _ := os.Stat(PostgresPath()); secretProtectionAvailable == false && fi.Mode().Perm() != 0o600 {
		t.Fatalf("postgres.json mode = %v", fi.Mode().Perm())
	}
	got, ok, err := LoadPGConfig()
	if err != nil || !ok || got != c {
		t.Fatalf("load = %+v ok=%v err=%v, want %+v", got, ok, err, c)
	}
	if err := ClearPGConfig(); err != nil {
		t.Fatal(err)
	}
	if err := ClearPGConfig(); err != nil {
		t.Fatal("clear twice should succeed")
	}
}

// PAGEVAULT_TEST_PG=postgres://... 가 있으면 실제 서버로 동기화를 검증한다.
func TestSyncIntegration(t *testing.T) {
	pgURL := os.Getenv("PAGEVAULT_TEST_PG")
	if pgURL == "" {
		t.Skip("PAGEVAULT_TEST_PG가 없어 건너뜀")
	}
	st, now := openTestStore(t)
	table := "pagevault_test_" + time.Now().Format("150405")
	c, err := NewPGConfig(pgURL, table)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() {
		conn, err := connectPG(ctx, c)
		if err == nil {
			conn.Exec(ctx, "DROP TABLE IF EXISTS "+quoteTable(table))
			conn.Close(ctx)
		}
	})
	tg, _ := st.AddTarget("https://a.com", "A")
	for i := 0; i < 250; i++ {
		*now = now.Add(time.Second)
		st.SaveCapture(okCapture(tg.ID, tg.URL, "T", strings.Repeat("x", i), "<html/>"))
	}
	res, err := Sync(ctx, st, c)
	if err != nil || res.Sent != 250 || res.Remaining != 0 {
		t.Fatalf("sync = %+v, %v", res, err)
	}
	// 같은 내용 재수집 → last_seen_at만 갱신되어 다시 upsert
	*now = now.Add(time.Second)
	st.SaveCapture(okCapture(tg.ID, tg.URL, "T", strings.Repeat("x", 249), "<html/>"))
	res, err = Sync(ctx, st, c)
	if err != nil || res.Sent != 1 {
		t.Fatalf("resync = %+v, %v", res, err)
	}
	conn, err := connectPG(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var n int
	var lastSeen time.Time
	conn.QueryRow(ctx, "SELECT count(*), max(last_seen_at) FROM "+quoteTable(table)).Scan(&n, &lastSeen)
	if n != 250 || !lastSeen.Equal(now.Truncate(time.Microsecond)) {
		t.Fatalf("rows=%d lastSeen=%v want %v", n, lastSeen, *now)
	}
}
