package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/gen2brain/beeep"

	"pagevault/core"
)

const trayRefreshInterval = 15 * time.Second

type tray struct {
	store *core.Store

	mStatus, mLogin, mLast, mOpen, mSync, mQuit *systray.MenuItem

	mu            sync.Mutex
	syncing       bool
	loginNotified map[int64]string // 대상 ID → 알림 보낸 시점의 last_seen_at
}

func runTray() {
	release, ok := acquireSingleInstance("PageVaultTray")
	if !ok {
		// 이미 트레이가 떠 있으면 관리 창만 연다.
		openWindow()
		return
	}
	defer release()
	beeep.AppName = "PageVault"
	t := &tray{loginNotified: map[int64]string{}}
	systray.Run(t.onReady, t.onExit)
}

func (t *tray) onReady() {
	if runtime.GOOS == "windows" {
		systray.SetIcon(iconICO)
	} else {
		systray.SetIcon(iconPNG)
	}
	systray.SetTooltip("PageVault")

	t.mStatus = systray.AddMenuItem("상태 확인 중…", "")
	t.mStatus.Disable()
	t.mLast = systray.AddMenuItem("마지막 수집: -", "")
	t.mLast.Disable()
	t.mLogin = systray.AddMenuItem("", "로그인이 만료된 대상이 있습니다. Chrome에서 다시 로그인하세요.")
	t.mLogin.Hide()
	systray.AddSeparator()
	t.mOpen = systray.AddMenuItem("관리 창 열기", "대상 URL, 수집 주기, Postgres 설정")
	t.mSync = systray.AddMenuItem("지금 동기화", "미동기화 스냅샷을 Postgres로 보냅니다")
	systray.AddSeparator()
	t.mQuit = systray.AddMenuItem("종료", "트레이 앱을 종료합니다 (수집은 Chrome 확장이 계속합니다)")
	systray.SetOnTapped(openWindow)

	st, err := core.OpenStore()
	if err != nil {
		t.mStatus.SetTitle("저장소 오류: " + err.Error())
	} else {
		t.store = st
	}

	go t.loop()
}

func (t *tray) onExit() {
	if t.store != nil {
		t.store.Close()
	}
}

func (t *tray) loop() {
	t.refresh()
	ticker := time.NewTicker(trayRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.refresh()
		case <-t.mOpen.ClickedCh:
			openWindow()
		case <-t.mLogin.ClickedCh:
			openWindow()
		case <-t.mSync.ClickedCh:
			go t.syncNow()
		case <-t.mQuit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func (t *tray) refresh() {
	if t.store == nil {
		return
	}
	s, err := t.store.Status()
	if err != nil {
		t.mStatus.SetTitle("상태 오류: " + err.Error())
		return
	}
	t.mStatus.SetTitle(fmt.Sprintf("대상 %d개 · 스냅샷 %d개 · 미동기화 %d개", s.EnabledCount, s.Snapshots, s.Unsynced))
	t.mLast.SetTitle("마지막 수집: " + localTime(s.LastCapture))
	tip := fmt.Sprintf("PageVault — 미동기화 %d개", s.Unsynced)
	if n := len(s.LoginRequired); n > 0 {
		t.mLogin.SetTitle(fmt.Sprintf("⚠ 로그인 필요 %d개", n))
		t.mLogin.Show()
		tip = fmt.Sprintf("PageVault — 로그인 필요 %d개", n)
	} else {
		t.mLogin.Hide()
	}
	systray.SetTooltip(tip)
	t.notifyLogin(s.LoginRequired)
}

// notifyLogin은 새로 로그인 필요 상태가 된 대상만 알린다.
func (t *tray) notifyLogin(states []core.TargetState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	current := map[int64]bool{}
	for _, st := range states {
		current[st.ID] = true
		if _, done := t.loginNotified[st.ID]; done {
			continue
		}
		t.loginNotified[st.ID] = st.LastSeenAt
		name := st.Name
		if name == "" {
			name = st.URL
		}
		_ = beeep.Notify("PageVault: 로그인 필요", name+"\nChrome에서 다시 로그인해야 수집됩니다.", iconPNG)
	}
	// 다시 정상 수집되면 기록을 지워 다음 만료 때 또 알린다.
	for id := range t.loginNotified {
		if !current[id] {
			delete(t.loginNotified, id)
		}
	}
}

func (t *tray) syncNow() {
	t.mu.Lock()
	if t.syncing || t.store == nil {
		t.mu.Unlock()
		return
	}
	t.syncing = true
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.syncing = false
		t.mu.Unlock()
	}()

	t.mSync.Disable()
	t.mSync.SetTitle("동기화 중…")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := core.SyncConfigured(ctx, t.store)
	t.mSync.SetTitle("지금 동기화")
	t.mSync.Enable()

	switch {
	case err != nil:
		_ = beeep.Notify("PageVault: 동기화 실패", err.Error(), iconPNG)
	case !res.PostgresConfigured:
		_ = beeep.Notify("PageVault", "Postgres가 설정되지 않았습니다. 관리 창에서 설정하세요.", iconPNG)
	default:
		_ = beeep.Notify("PageVault: 동기화 완료", fmt.Sprintf("보냄 %d개, 남음 %d개", res.Sent, res.Remaining), iconPNG)
	}
	t.refresh()
}

func openWindow() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	// 관리 창은 Wails SingleInstanceLock으로 이미 열려 있으면 그 창을 앞으로 가져온다.
	_ = exec.Command(self, "--window").Start()
}

func localTime(s string) string {
	t, err := core.ParseTime(s)
	if err != nil {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
