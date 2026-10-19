// pagevault는 PageVault CLI이자 Chrome Native Messaging 호스트다.
// Chrome이 chrome-extension://<id>/ 인자로 실행하면 native-host 모드로 동작한다.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"pagevault/core"
)

const usage = `PageVault — 로그인된 Chrome 페이지를 주기적으로 백업합니다.

사용법:
  pagevault status
  pagevault target list
  pagevault target add <url> [이름]
  pagevault target remove|enable|disable <id>
  pagevault interval [초]          수집 주기 (최소 30초)
  pagevault delay [초]             로딩 후 대기 (0~120초)
  pagevault snapshots [개수] [--url <url>]
  pagevault show <id> [--html]
  pagevault pg set <postgres://user:pass@host:5432/db?sslmode=prefer> [--table 이름]
  pagevault pg show|test|clear
  pagevault sync
  pagevault install-host [확장ID...]
  pagevault uninstall-host
  pagevault native-host            (Chrome이 실행하는 모드)

데이터 위치: %s
`

func main() {
	args := os.Args[1:]
	// Chrome은 호스트를 "chrome-extension://<id>/" (Windows는 --parent-window=... 도) 인자로 실행한다.
	if len(args) > 0 && (args[0] == "native-host" || strings.HasPrefix(args[0], "chrome-extension://")) {
		os.Exit(runNativeHost())
	}
	if err := run(args); err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		os.Exit(1)
	}
}

func runNativeHost() int {
	logger, closer := core.OpenLog("[host] ")
	defer closer.Close()
	st, err := core.OpenStore()
	if err != nil {
		logger.Printf("저장소 열기 실패: %v", err)
		return 1
	}
	defer st.Close()
	logger.Printf("시작 pid=%d args=%q", os.Getpid(), os.Args[1:])
	h := &core.Host{Store: st, Log: logger}
	// Go는 Windows에서도 stdin/stdout을 텍스트 변환 없이 바이너리로 다룬다.
	if err := h.Serve(os.Stdin, os.Stdout); err != nil {
		logger.Printf("종료(오류): %v", err)
		return 1
	}
	logger.Printf("종료")
	return 0
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Printf(usage, core.Home())
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version", "--version":
		fmt.Println("pagevault", core.Version)
		return nil
	case "install-host":
		return installHost(rest)
	case "uninstall-host":
		removed, err := core.UninstallHost()
		for _, r := range removed {
			fmt.Println("제거:", r)
		}
		if err == nil && len(removed) == 0 {
			fmt.Println("등록된 호스트가 없습니다.")
		}
		return err
	case "pg":
		return pgCmd(rest)
	}

	st, err := core.OpenStore()
	if err != nil {
		return err
	}
	defer st.Close()

	switch cmd {
	case "status":
		return status(st)
	case "target":
		return targetCmd(st, rest)
	case "interval":
		return intSetting(rest, "수집 주기", "초", st.SetInterval, func(s core.Settings) int { return s.IntervalSeconds }, st)
	case "delay":
		return intSetting(rest, "로딩 대기", "초", st.SetLoadDelay, func(s core.Settings) int { return s.LoadDelaySeconds }, st)
	case "snapshots":
		return snapshots(st, rest)
	case "show":
		return show(st, rest)
	case "sync":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := core.SyncConfigured(ctx, st)
		if !res.PostgresConfigured && err == nil {
			fmt.Printf("Postgres가 설정되지 않았습니다 (미동기화 %d개). pagevault pg set 으로 설정하세요.\n", res.Remaining)
			return nil
		}
		fmt.Printf("보냄 %d개, 남음 %d개\n", res.Sent, res.Remaining)
		return err
	}
	return fmt.Errorf("알 수 없는 명령: %s (pagevault help)", cmd)
}

func status(st *core.Store) error {
	s, err := st.Status()
	if err != nil {
		return err
	}
	fmt.Println("데이터 위치:", s.Home)
	fmt.Printf("대상: %d개 (사용 %d개)\n", s.Targets, s.EnabledCount)
	fmt.Printf("수집 주기: %d초, 로딩 대기: %d초\n", s.Settings.IntervalSeconds, s.Settings.LoadDelaySeconds)
	fmt.Printf("스냅샷: %d개, 미동기화: %d개\n", s.Snapshots, s.Unsynced)
	fmt.Println("마지막 수집:", orDash(localTime(s.LastCapture)))
	fmt.Println("Postgres:", pgSummary())
	for _, t := range s.LoginRequired {
		fmt.Printf("로그인 필요: #%d %s %s\n", t.ID, t.Name, t.URL)
	}
	return nil
}

func pgSummary() string {
	c, ok, err := core.LoadPGConfig()
	switch {
	case err != nil:
		return "설정 오류: " + err.Error()
	case !ok:
		return "설정 안 됨"
	}
	return c.Masked() + " (테이블 " + c.Table + ")"
}

func targetCmd(st *core.Store, args []string) error {
	if len(args) == 0 || args[0] == "list" {
		states, err := st.TargetStates()
		if err != nil {
			return err
		}
		if len(states) == 0 {
			fmt.Println("등록된 대상이 없습니다. pagevault target add <url> [이름]")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "ID\t사용\t이름\t마지막 상태\t마지막 확인\tURL")
		for _, t := range states {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", t.ID, yesNo(t.Enabled), orDash(t.Name),
				orDash(t.LastStatus), orDash(localTime(t.LastSeenAt)), t.URL)
		}
		return w.Flush()
	}
	switch args[0] {
	case "add":
		if len(args) < 2 {
			return errors.New("사용법: pagevault target add <url> [이름]")
		}
		t, err := st.AddTarget(args[1], strings.Join(args[2:], " "))
		if err != nil {
			return err
		}
		fmt.Printf("추가: #%d %s\n", t.ID, t.URL)
		return nil
	case "remove", "enable", "disable":
		if len(args) != 2 {
			return fmt.Errorf("사용법: pagevault target %s <id>", args[0])
		}
		id, err := parseID(args[1])
		if err != nil {
			return err
		}
		switch args[0] {
		case "remove":
			err = st.RemoveTarget(id)
		case "enable":
			err = st.SetTargetEnabled(id, true)
		default:
			err = st.SetTargetEnabled(id, false)
		}
		if err == nil {
			fmt.Println("완료")
		}
		return err
	}
	return fmt.Errorf("알 수 없는 target 명령: %s", args[0])
}

func intSetting(args []string, label, unit string, set func(int) error, get func(core.Settings) int, st *core.Store) error {
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("숫자가 아닙니다: %q", args[0])
		}
		if err := set(n); err != nil {
			return err
		}
	}
	s, err := st.Settings()
	if err != nil {
		return err
	}
	fmt.Printf("%s: %d%s\n", label, get(s), unit)
	if len(args) > 0 && label == "수집 주기" {
		fmt.Println("(확장은 다음 라운드부터 새 주기를 적용합니다)")
	}
	return nil
}

func snapshots(st *core.Store, args []string) error {
	limit, url := 20, ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--url" && i+1 < len(args):
			url = args[i+1]
			i++
		default:
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return fmt.Errorf("개수가 올바르지 않습니다: %q", args[i])
			}
			limit = n
		}
	}
	list, err := st.Snapshots(limit, url)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("스냅샷이 없습니다.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ID\t상태\t수집\t마지막 확인\t동기화\t제목\tURL")
	for _, s := range list {
		synced := "대기"
		if s.SyncedAt != "" {
			synced = "완료"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, s.Status, localTime(s.CapturedAt), localTime(s.LastSeenAt),
			synced, truncate(orDash(s.Title), 40), s.TargetURL)
	}
	return w.Flush()
}

func show(st *core.Store, args []string) error {
	if len(args) < 1 {
		return errors.New("사용법: pagevault show <id> [--html]")
	}
	id, err := parseID(args[0])
	if err != nil {
		return err
	}
	s, err := st.Snapshot(id)
	if err != nil {
		return err
	}
	if len(args) > 1 && args[1] == "--html" {
		fmt.Print(s.HTML)
		return nil
	}
	fmt.Printf("#%d %s\n", s.ID, s.UID)
	fmt.Println("상태:", s.Status)
	fmt.Println("대상:", orDash(s.TargetName), s.TargetURL)
	fmt.Println("최종 URL:", orDash(s.FinalURL))
	fmt.Println("제목:", orDash(s.Title))
	fmt.Println("수집:", localTime(s.CapturedAt), "/ 마지막 확인:", localTime(s.LastSeenAt))
	fmt.Println("동기화:", orDash(localTime(s.SyncedAt)))
	if s.Error != "" {
		fmt.Println("오류:", s.Error)
	}
	if s.Status == core.StatusOK {
		fmt.Printf("HTML: %d바이트 (--html 로 출력)\n\n", len(s.HTML))
		fmt.Println(s.Text)
	}
	return nil
}

func pgCmd(args []string) error {
	if len(args) == 0 {
		args = []string{"show"}
	}
	switch args[0] {
	case "show":
		fmt.Println(pgSummary())
		return nil
	case "set":
		if len(args) < 2 {
			return errors.New("사용법: pagevault pg set <postgres://...> [--table 이름]")
		}
		table := ""
		if len(args) >= 4 && args[2] == "--table" {
			table = args[3]
		} else if len(args) > 2 {
			return errors.New("사용법: pagevault pg set <postgres://...> [--table 이름]")
		}
		c, err := core.NewPGConfig(args[1], table)
		if err != nil {
			return err
		}
		if err := core.SavePGConfig(c); err != nil {
			return err
		}
		fmt.Println("저장:", c.Masked(), "(테이블 "+c.Table+")")
		return nil
	case "test":
		c, ok, err := core.LoadPGConfig()
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("Postgres가 설정되지 않았습니다")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		v, err := core.TestPG(ctx, c)
		if err != nil {
			return err
		}
		fmt.Printf("접속 성공: PostgreSQL %s, 테이블 %s 준비됨\n", v, c.Table)
		return nil
	case "clear":
		if err := core.ClearPGConfig(); err != nil {
			return err
		}
		fmt.Println("Postgres 설정을 지웠습니다.")
		return nil
	}
	return fmt.Errorf("알 수 없는 pg 명령: %s", args[0])
}

func installHost(ids []string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	res, err := core.InstallHost(self, ids)
	if err != nil {
		return err
	}
	fmt.Println("실행 파일:", res.ExePath)
	fmt.Println("매니페스트:", res.ManifestPath)
	fmt.Println("등록:", res.Registered)
	fmt.Println("Chrome에서 확장 프로그램을 다시 로드하면 적용됩니다.")
	return nil
}

func parseID(s string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimPrefix(s, "#"), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("ID가 올바르지 않습니다: %q", s)
	}
	return id, nil
}

func localTime(s string) string {
	t, err := core.ParseTime(s)
	if err != nil {
		return s
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "예"
	}
	return "아니오"
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
