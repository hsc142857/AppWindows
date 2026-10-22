// activitylogkeeper는 ActivityLogKeeper CLI이자 Chrome Native Messaging 호스트다.
// Chrome이 chrome-extension://<id>/ 인자로 실행하면 native-host 모드로 동작한다.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"activitylogkeeper/core"
)

const usage = `ActivityLogKeeper — 웹 프로그램의 활동 로그(이력 표)를 주기적으로 읽어 PC에 보관합니다.

사용법:
  activitylogkeeper-cli status
  activitylogkeeper-cli target list
  activitylogkeeper-cli target add <url> [이름] [--selector <CSS>]   (선택자 생략: 행이 가장 많은 표)
  activitylogkeeper-cli target selector <id> [CSS]                   (CSS 생략: 자동으로 되돌림)
  activitylogkeeper-cli target rename <id> <이름>
  activitylogkeeper-cli target remove|enable|disable <id>            (remove해도 모은 로그는 남음)
  activitylogkeeper-cli interval [초]          수집 주기 (최소 30초)
  activitylogkeeper-cli delay [초]             로딩 후 대기 (0~120초)
  activitylogkeeper-cli captures [개수] [--url <url>]     수집 기록 (언제 몇 행이 보였는지)
  activitylogkeeper-cli entries [개수] [--url <url>]      최근 로그 행
  activitylogkeeper-cli export [--url <url>] [--from YYYY-MM-DD] [--to YYYY-MM-DD] [--out 파일.csv]
  activitylogkeeper-cli api set <https://api.example.com> [토큰]   (토큰 생략 시 입력받음)
  activitylogkeeper-cli api show|test|clear
  activitylogkeeper-cli sync [--retry-rejected]
  activitylogkeeper-cli install-host [확장ID...]
  activitylogkeeper-cli uninstall-host
  activitylogkeeper-cli native-host            (Chrome이 실행하는 모드)

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
		fmt.Println("activitylogkeeper", core.Version)
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
	case "api":
		return apiCmd(rest)
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
	case "captures":
		return captures(st, rest)
	case "entries":
		return entries(st, rest)
	case "export":
		return export(st, rest)
	case "sync":
		if len(rest) > 0 && rest[0] == "--retry-rejected" {
			n, err := st.ClearRejected()
			if err != nil {
				return err
			}
			fmt.Printf("거부된 %d개를 다시 보냅니다.\n", n)
		} else if len(rest) > 0 {
			return errors.New("사용법: activitylogkeeper-cli sync [--retry-rejected]")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := core.SyncConfigured(ctx, st)
		if !res.APIConfigured && err == nil {
			fmt.Printf("Web API가 설정되지 않았습니다 (미전송 %d행, 로컬에는 저장되어 있음). activitylogkeeper-cli api set 으로 설정하세요.\n", res.Remaining)
			return nil
		}
		fmt.Printf("보냄 %d행, 거부 %d행, 남음 %d행\n", res.Sent, res.Rejected, res.Remaining)
		return err
	}
	return fmt.Errorf("알 수 없는 명령: %s (activitylogkeeper-cli help)", cmd)
}

func status(st *core.Store) error {
	s, err := st.Status()
	if err != nil {
		return err
	}
	fmt.Println("데이터 위치:", s.Home)
	fmt.Printf("대상: %d개 (사용 %d개)\n", s.Targets, s.EnabledCount)
	fmt.Printf("수집 주기: %d초, 로딩 대기: %d초\n", s.Settings.IntervalSeconds, s.Settings.LoadDelaySeconds)
	fmt.Printf("로그: %d행, 서버 미전송: %d행\n", s.Entries, s.Unsynced)
	if s.Rejected > 0 {
		fmt.Printf("서버가 거부: %d행 (activitylogkeeper-cli entries 로 사유 확인, sync --retry-rejected 로 재전송)\n", s.Rejected)
	}
	fmt.Println("마지막 수집:", orDash(localTime(s.LastCapture)))
	summary, _ := core.APISummary()
	fmt.Println("Web API:", summary)
	for _, t := range s.NeedAttention {
		fmt.Printf("확인 필요: #%d %s %s — %s\n", t.ID, orDash(t.Name), t.URL, statusText(t.LastStatus, t.LastError))
	}
	return nil
}

func targetCmd(st *core.Store, args []string) error {
	if len(args) == 0 || args[0] == "list" {
		states, err := st.TargetStates()
		if err != nil {
			return err
		}
		if len(states) == 0 {
			fmt.Println("등록된 대상이 없습니다. activitylogkeeper-cli target add <url> [이름] [--selector <CSS>]")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "ID\t사용\t이름\t로그\t마지막 상태\t마지막 수집\t선택자\tURL")
		for _, t := range states {
			fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n", t.ID, yesNo(t.Enabled), orDash(t.Name), t.Entries,
				orDash(t.LastStatus), orDash(localTime(t.LastCapturedAt)), orAuto(t.Selector), t.URL)
		}
		return w.Flush()
	}
	switch args[0] {
	case "add":
		rest, selector := takeFlag(args[1:], "--selector")
		if len(rest) < 1 {
			return errors.New("사용법: activitylogkeeper-cli target add <url> [이름] [--selector <CSS>]")
		}
		t, err := st.AddTarget(rest[0], strings.Join(rest[1:], " "), selector)
		if err != nil {
			return err
		}
		fmt.Printf("추가: #%d %s (선택자: %s)\n", t.ID, t.URL, orAuto(t.Selector))
		return nil
	case "selector", "rename":
		if len(args) < 2 {
			return fmt.Errorf("사용법: activitylogkeeper-cli target %s <id> <값>", args[0])
		}
		id, err := parseID(args[1])
		if err != nil {
			return err
		}
		v := strings.Join(args[2:], " ")
		if args[0] == "selector" {
			err = st.SetTargetSelector(id, v)
		} else {
			err = st.RenameTarget(id, v)
		}
		if err == nil {
			fmt.Println("완료")
		}
		return err
	case "remove", "enable", "disable":
		if len(args) != 2 {
			return fmt.Errorf("사용법: activitylogkeeper-cli target %s <id>", args[0])
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

// listArgs는 "[개수] [--url <url>]" 인자를 읽는다.
func listArgs(args []string) (limit int, url string, err error) {
	args, url = takeFlag(args, "--url")
	limit = 20
	for _, a := range args {
		n, err := strconv.Atoi(a)
		if err != nil || n <= 0 {
			return 0, "", fmt.Errorf("개수가 올바르지 않습니다: %q", a)
		}
		limit = n
	}
	return limit, url, nil
}

func captures(st *core.Store, args []string) error {
	limit, url, err := listArgs(args)
	if err != nil {
		return err
	}
	list, err := st.Captures(limit, url)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("수집 기록이 없습니다.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ID\t수집\t상태\t행\t새 행\t대상\tURL")
	for _, c := range list {
		fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%d\t%s\t%s\n", c.ID, localTime(c.CapturedAt), statusText(c.Status, c.Error),
			c.RowCount, c.NewRows, orDash(c.TargetName), c.TargetURL)
	}
	return w.Flush()
}

func entries(st *core.Store, args []string) error {
	limit, url, err := listArgs(args)
	if err != nil {
		return err
	}
	list, err := st.Entries(core.EntryFilter{TargetURL: url, Limit: limit, Newest: true})
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("저장된 로그가 없습니다.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ID\t처음 확인\t마지막 확인\t서버\t대상\t내용")
	for _, e := range list {
		synced := "대기"
		switch {
		case e.SyncedAt != "":
			synced = "완료"
		case e.SyncError != "":
			synced = "거부(" + e.SyncError + ")"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", e.ID, localTime(e.FirstSeenAt), localTime(e.LastSeenAt), synced,
			orDash(e.TargetName), truncate(e.Text(), 80))
	}
	return w.Flush()
}

// export는 로그를 CSV로 내보낸다. 날짜는 PC 시간대 기준이며 --to 날짜도 포함한다.
func export(st *core.Store, args []string) error {
	args, url := takeFlag(args, "--url")
	args, from := takeFlag(args, "--from")
	args, to := takeFlag(args, "--to")
	args, out := takeFlag(args, "--out")
	if len(args) > 0 {
		return errors.New("사용법: activitylogkeeper-cli export [--url <url>] [--from YYYY-MM-DD] [--to YYYY-MM-DD] [--out 파일.csv]")
	}
	f := core.EntryFilter{TargetURL: url}
	var err error
	if f.From, err = dayStart(from, 0); err != nil {
		return err
	}
	if f.To, err = dayStart(to, 1); err != nil {
		return err
	}
	list, err := st.Entries(f)
	if err != nil {
		return err
	}
	w := io.Writer(os.Stdout)
	if out != "" {
		file, err := os.Create(out)
		if err != nil {
			return err
		}
		defer file.Close()
		w = file
	}
	if err := core.WriteCSV(w, list, time.Local); err != nil {
		return err
	}
	if out != "" {
		fmt.Printf("%d행을 %s에 저장했습니다.\n", len(list), out)
	}
	return nil
}

// dayStart는 YYYY-MM-DD(PC 시간대)에 addDays를 더한 날의 0시를 저장 형식(UTC)으로 돌려준다. 빈 값이면 "".
func dayStart(s string, addDays int) (string, error) {
	if s == "" {
		return "", nil
	}
	d, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return "", fmt.Errorf("날짜는 YYYY-MM-DD 형식이어야 합니다: %q", s)
	}
	return core.FormatTime(d.AddDate(0, 0, addDays)), nil
}

// takeFlag는 args에서 "name 값"을 꺼내고 나머지를 돌려준다.
func takeFlag(args []string, name string) (rest []string, value string) {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			value = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	return rest, value
}

func statusText(status, errMsg string) string {
	switch status {
	case core.StatusOK:
		return "정상"
	case core.StatusEmpty:
		return "표를 찾지 못함 (선택자 확인)"
	case core.StatusLoginRequired:
		return "로그인 필요"
	case core.StatusError:
		return "오류: " + truncate(errMsg, 60)
	}
	return orDash(status)
}

func orAuto(s string) string {
	if s == "" {
		return "자동"
	}
	return s
}

func apiCmd(args []string) error {
	if len(args) == 0 {
		args = []string{"show"}
	}
	switch args[0] {
	case "show":
		summary, _ := core.APISummary()
		fmt.Println(summary)
		return nil
	case "set":
		if len(args) < 2 || len(args) > 3 {
			return errors.New("사용법: activitylogkeeper-cli api set <https://api.example.com> [토큰]  (토큰을 생략하면 입력받습니다)")
		}
		token := ""
		if len(args) == 3 {
			token = args[2]
		} else {
			var err error
			if token, err = readSecret("API 토큰: "); err != nil {
				return err
			}
		}
		c, err := core.NewAPIConfig(args[1], token)
		if err != nil {
			return err
		}
		if err := core.SaveAPIConfig(c); err != nil {
			return err
		}
		fmt.Println("저장:", c.BaseURL, "(토큰 "+c.MaskedToken()+")")
		return nil
	case "test":
		c, ok, err := core.LoadAPIConfig()
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("Web API가 설정되지 않았습니다 (activitylogkeeper-cli api set)")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		v, err := core.TestAPI(ctx, c)
		if err != nil {
			return err
		}
		fmt.Printf("접속 성공: %s (서버 버전 %s)\n", c.BaseURL, orDash(v))
		return nil
	case "clear":
		if err := core.ClearAPIConfig(); err != nil {
			return err
		}
		fmt.Println("Web API 설정을 지웠습니다.")
		return nil
	}
	return fmt.Errorf("알 수 없는 api 명령: %s", args[0])
}

// readSecret은 터미널이면 화면에 표시하지 않고, 아니면(파이프) 표준 입력 한 줄을 읽는다.
func readSecret(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, prompt)
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return strings.TrimSpace(string(b)), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("토큰을 읽지 못했습니다")
	}
	return strings.TrimSpace(line), nil
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
