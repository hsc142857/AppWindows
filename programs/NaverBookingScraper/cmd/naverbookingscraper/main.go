// naverbookingscraper는 NaverBookingScraper CLI이자 Chrome Native Messaging 호스트다.
// 네이버 예약을 Motospec Visit Import API로 올린다.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"appwindows/shared/apiclient"
	"appwindows/shared/secret"
	"naverbookingscraper/core"
)

const usage = `NaverBookingScraper %s — 네이버 예약을 수집해 Motospec Visit Import API로 올립니다.

사용법:
  naverbookingscraper-cli status               실행 확인: 서버 연결, 인증키를 발급한 업체
  naverbookingscraper-cli api set [주소]       인증키 입력·저장 (한 번만, 주소 생략 시 %s)
  naverbookingscraper-cli api show|test|clear
  naverbookingscraper-cli visits [날짜]         서버에 저장된 그날 예약 (YYYY-MM-DD, 생략 시 오늘)
  naverbookingscraper-cli visit <예약번호>      서버에 저장된 예약 한 건
  naverbookingscraper-cli import <예약.json> [--dry-run]
                                           예약 JSON을 저장하고 전송 (긁어 온 데이터 대신, GUI 전송 현황에 나옴)
                                           --dry-run: 저장·전송 없이 서버 예상 결과만

구현 예정: business list|add|remove, interval, reservations, upload,
          install-host, uninstall-host, native-host (Chrome이 실행하는 모드)

데이터 위치: %s
`

func main() {
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "native-host" || strings.HasPrefix(args[0], "chrome-extension://")) {
		// TODO: core 호스트 구현 후 연결 (PageVault cmd/pagevault runNativeHost 참고).
		// 이 모드는 stdin이 Chrome 연결이므로 인증키를 물어볼 수 없다 — 키가 없으면 오류로 보고한다.
		os.Exit(1)
	}
	if err := run(args); err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Printf(usage, core.Version, core.DefaultBaseURL, core.Home())
		return nil
	}
	switch args[0] {
	case "version", "--version":
		fmt.Println("naverbookingscraper", core.Version)
		return nil
	case "status":
		return status()
	case "api":
		return apiCmd(args[1:])
	case "visits":
		return visits(args[1:])
	case "visit":
		return visit(args[1:])
	case "import":
		return importFile(args[1:])
	}
	return fmt.Errorf("알 수 없는 명령: %s (naverbookingscraper-cli help)", args[0])
}

func apiCmd(args []string) error {
	if len(args) == 0 {
		args = []string{"show"}
	}
	switch args[0] {
	case "show":
		c, err := core.LoadAPIConfig()
		if err != nil {
			return err
		}
		fmt.Printf("주소: %s\n인증키: %s\n", c.BaseURL, c.MaskedToken())
		if p, ok, _ := core.LoadPartner(); ok {
			fmt.Printf("업체: %s (마지막 확인 %s)\n", p.Label(), localRFC3339(p.CheckedAt))
		}
		return nil
	case "set":
		if len(args) > 2 {
			return errors.New("사용법: naverbookingscraper-cli api set [주소]  (인증키는 입력받습니다)")
		}
		base := ""
		if len(args) == 2 {
			base = args[1]
		}
		_, err := promptAndSave(base)
		return err
	case "test":
		return status()
	case "clear":
		if err := core.ClearAPIConfig(); err != nil {
			return err
		}
		fmt.Println("인증키와 업체 정보를 지웠습니다. 다음에 필요할 때 다시 입력받습니다.")
		return nil
	}
	return fmt.Errorf("알 수 없는 api 명령: %s", args[0])
}

// status는 실행할 때 확인할 것을 보여 준다: 서버 연결, 인증키, 키를 발급한 업체.
// 여러 업체가 같은 프로그램을 쓰므로 업체가 바뀌었으면 경고한다.
func status() error {
	c, err := ensureConfig()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ping, err := core.Ping(ctx, c)
	if err != nil {
		return err
	}
	p, change, err := core.RememberPartner(ping.PartnerID, ping.PartnerName)
	if err != nil {
		return err
	}
	fmt.Printf("서버: %s (연결됨, 권한 %s)\n", c.BaseURL, strings.Join(ping.Scopes, ", "))
	fmt.Printf("인증키: %s\n", c.MaskedToken())
	printPartner(p, change)
	if st, err := core.OpenStore(); err == nil {
		_, c, err := st.List(p.ID)
		st.Close()
		if err == nil {
			fmt.Printf("전송 현황: 완료 %d, 미전송 %d (전송 실패 %d, 거부 %d, 다른 업체 %d)\n",
				c.Sent, c.Pending+c.Rejected+c.OtherPartner, c.Failed, c.Rejected, c.OtherPartner)
		}
	}
	fmt.Printf("데이터 위치: %s\n", core.Home())
	return nil
}

// printPartner는 업체명을 보여 주고, 기억해 둔 업체와 다르면 경고한다.
func printPartner(p core.Partner, change core.PartnerChange) {
	fmt.Printf("업체: %s (ID %s)\n", p.Label(), orDash(p.ID))
	if change.Changed {
		fmt.Fprintf(os.Stderr, "⚠ 인증키의 업체가 바뀌었습니다: %s → %s\n", change.Previous.Label(), p.Label())
	}
}

// notePartner는 GET 응답에 온 업체를 기억하고 화면 맨 위에 보여 준다.
func notePartner(id, name string) {
	if id == "" {
		return
	}
	p, change, _ := core.RememberPartner(id, name)
	printPartner(p, change)
}

func localRFC3339(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return orDash(s)
	}
	return t.In(core.Seoul).Format("2006-01-02 15:04")
}

// ensureConfig는 저장된 설정을 돌려준다. 아직 없고 터미널이면 한 번 입력받아 저장한다.
func ensureConfig() (apiclient.Config, error) {
	c, err := core.LoadAPIConfig()
	if !errors.Is(err, core.ErrNoAPIKey) {
		return c, err
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return c, err
	}
	if secret.Available {
		fmt.Fprintln(os.Stderr, "인증키가 아직 없습니다. 한 번만 입력하면 이 PC에 암호화해 저장합니다.")
	} else {
		fmt.Fprintln(os.Stderr, "인증키가 아직 없습니다. 한 번만 입력하면 저장합니다 (이 OS는 암호화 없이 본인만 읽을 수 있는 파일로 보관).")
	}
	return promptAndSave("")
}

// promptAndSave는 인증키를 입력받아 연결을 확인한 뒤 저장한다. 키 값은 화면·로그에 남기지 않는다.
func promptAndSave(base string) (apiclient.Config, error) {
	if base == "" && term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr, "API 주소 [%s]: ", core.DefaultBaseURL)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		base = strings.TrimSpace(line)
	}
	prompt := "인증키 (msvi_…): "
	old, oldErr := core.LoadAPIConfig()
	if oldErr == nil {
		prompt = "인증키 (Enter: 저장된 키 " + old.MaskedToken() + " 유지): "
	}
	key, err := readSecret(prompt) // 비우면(Enter) SetupAPI가 저장된 키를 유지한다
	if err != nil && !(oldErr == nil && key == "") {
		return apiclient.Config{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, p, change, err := core.SetupAPI(ctx, base, key)
	if err != nil {
		return c, err
	}
	fmt.Printf("저장: %s (인증키 %s)\n", c.BaseURL, c.MaskedToken())
	printPartner(p, change)
	return c, nil
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
		return "", errors.New("인증키를 읽지 못했습니다")
	}
	return strings.TrimSpace(line), nil
}

func visits(args []string) error {
	if len(args) > 1 {
		return errors.New("사용법: naverbookingscraper-cli visits [날짜]  (예: 2026-10-20, 생략 시 오늘)")
	}
	date := time.Now().In(core.Seoul).Format("2006-01-02")
	if len(args) == 1 {
		date = args[0]
	}
	c, err := ensureConfig()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := core.ListDay(ctx, c, date)
	if err != nil {
		return err
	}
	notePartner(res.PartnerID, res.PartnerName)
	if len(res.Visits) == 0 {
		fmt.Printf("%s: 예약이 없습니다.\n", res.Date)
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "예약번호\t상태\t시각\t이름\t차량\t요청")
	for _, v := range res.Visits {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", v.ExternalBookingID, v.Status, seoulTime(v.ScheduledAt, "15:04"),
			v.Name, carText(v), strings.ReplaceAll(firstN(v.RequestNote, 30), "\n", " "))
	}
	fmt.Fprintf(w, "\n%s: %d건\n", res.Date, len(res.Visits))
	return w.Flush()
}

func visit(args []string) error {
	if len(args) != 1 {
		return errors.New("사용법: naverbookingscraper-cli visit <예약번호>")
	}
	c, err := ensureConfig()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	one, found, err := core.GetVisit(ctx, c, args[0])
	if err != nil {
		return err
	}
	if !found {
		fmt.Printf("서버에 예약 %s이(가) 없습니다.\n", args[0])
		return nil
	}
	notePartner(one.PartnerID, one.PartnerName)
	v := one.Visit
	year := "-"
	if v.Year != nil {
		year = fmt.Sprint(*v.Year)
	}
	fmt.Printf("예약번호: %s\n상태: %s\n일시: %s\n이름: %s\n전화: %s\n차량: %s (연식 %s, 주행 %dkm)\n요청: %s\n출처: %s\n",
		v.ExternalBookingID, v.Status, seoulTime(v.ScheduledAt, "2006-01-02 15:04"), v.Name, v.Phone,
		orDash(carText(v)), year, v.Mileage, orDash(v.RequestNote), v.Source)
	return nil
}

// seoulTime은 서버 시각(UTC)을 서울 시각으로 보여 준다.
func seoulTime(s *string, layout string) string {
	if s == nil {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return *s
	}
	return t.In(core.Seoul).Format(layout)
}

func carText(v core.ListVisitItem) string {
	parts := []string{}
	for _, p := range []string{v.PlateNo, v.Brand, v.Model} {
		if p != "" && p != "NAVERIMPORT" { // 차량번호가 없으면 서버가 넣는 자리표시
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func importFile(args []string) error {
	dryRun := false
	if len(args) == 2 && args[1] == "--dry-run" {
		dryRun, args = true, args[:1]
	}
	if len(args) != 1 {
		return errors.New("사용법: naverbookingscraper-cli import <예약.json> [--dry-run]  (Reservation 배열)")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	var list []core.Reservation
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("JSON 형식 오류: %w", err)
	}
	c, err := ensureConfig()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// POST 응답에는 업체명이 없으므로, 보내기 전에 어느 업체로 들어가는지 확인해 보여 준다.
	p, change, err := core.CheckPartner(ctx, c)
	if err != nil {
		return err
	}
	printPartner(p, change)

	if dryRun {
		items := make([]core.ImportItem, len(list))
		for i, r := range list {
			items[i] = r.ToImportItem()
		}
		res, err := core.Import(ctx, c, items, true)
		fmt.Printf("[미리보기] %s에 쓰지 않았습니다. 실제로 보내면:\n", p.Label())
		fmt.Printf("등록 %d, 수정 %d, 변경 없음 %d, 취소 %d, 오류 %d\n", res.Created, res.Updated, res.Skipped, res.Cancelled, len(res.Errors))
		labels := map[string]string{"created": "등록", "updated": "수정", "skipped": "변경 없음", "cancelled": "취소", "error": "오류"}
		for _, r := range res.Results {
			line := fmt.Sprintf("  %s: %s", r.ExternalBookingID, labels[r.Result])
			if r.Error != "" {
				line += " — " + r.Error
			}
			fmt.Println(line)
		}
		return err
	}

	// 실제 전송은 GUI와 같은 로컬 저장소를 거친다: 저장 → 전송 → 서버가 처리한 것만 전송 완료.
	st, err := core.OpenStore()
	if err != nil {
		return err
	}
	defer st.Close()
	saved, err := st.SaveScraped(p.ID, list)
	if err != nil {
		return err
	}
	fmt.Printf("저장: 새 예약 %d, 변경 %d, 그대로 %d\n", saved.New, saved.Changed, saved.Unchanged)
	up, err := st.UploadPending(ctx, c, p.ID)
	if err != nil {
		return err
	}
	fmt.Printf("%s(으)로 전송 완료 %d건, 거부 %d건, 미전송 %d건\n", p.Label(), up.Sent, up.Rejected, up.Remaining)
	if up.Error != "" {
		fmt.Println("전송 실패 (다음에 다시 보냄):", up.Error)
	}
	return printUnsent(st, p.ID)
}

// printUnsent는 미전송(대기·전송 실패·거부·다른 업체)을 따로 보여 준다.
func printUnsent(st *core.Store, partnerID string) error {
	list, counts, err := st.List(partnerID)
	if err != nil {
		return err
	}
	unsent := counts.Pending + counts.Rejected + counts.OtherPartner
	if unsent == 0 {
		fmt.Printf("미전송 없음 (전송 완료 %d건)\n", counts.Sent)
		return nil
	}
	fmt.Printf("\n미전송 %d건:\n", unsent)
	for _, r := range list {
		reason := ""
		switch {
		case r.State == core.StateSent:
			continue
		case r.State == core.StateOtherPartner:
			reason = "다른 업체 — 보내지 않음"
		case r.State == core.StateRejected:
			reason = "거부: " + r.RejectError
		case r.LastError != "":
			reason = "전송 실패: " + r.LastError
		default:
			reason = "대기"
		}
		fmt.Printf("  %s  %s  %s\n", r.BookingID, orDash(r.Customer), reason)
	}
	return nil
}

func firstN(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
