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

	"activitylogkeeper/core"
)

const trayRefreshInterval = 15 * time.Second

type tray struct {
	store *core.Store

	mStatus, mAttention, mLast, mOpen, mSync, mQuit *systray.MenuItem

	mu       sync.Mutex
	syncing  bool
	notified map[int64]string // 대상 ID → 알림 보낸 상태 (login_required / empty)
}

func runTray() {
	release, ok := acquireSingleInstance("ActivityLogKeeperTray")
	if !ok {
		// 이미 트레이가 떠 있으면 관리 창만 연다.
		openWindow()
		return
	}
	defer release()
	beeep.AppName = "ActivityLogKeeper"
	t := &tray{notified: map[int64]string{}}
	systray.Run(t.onReady, t.onExit)
}

func (t *tray) onReady() {
	if runtime.GOOS == "windows" {
		systray.SetIcon(iconICO)
	} else {
		systray.SetIcon(iconPNG)
	}
	systray.SetTooltip("ActivityLogKeeper")

	t.mStatus = systray.AddMenuItem("상태 확인 중…", "")
	t.mStatus.Disable()
	t.mLast = systray.AddMenuItem("마지막 수집: -", "")
	t.mLast.Disable()
	t.mAttention = systray.AddMenuItem("", "로그인이 필요하거나 표를 찾지 못한 대상이 있습니다.")
	t.mAttention.Hide()
	systray.AddSeparator()
	t.mOpen = systray.AddMenuItem("관리 창 열기", "대상, 로그, 수집 기록, 설정")
	t.mSync = systray.AddMenuItem("지금 동기화", "미전송 로그를 Web API로 올립니다")
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
		case <-t.mAttention.ClickedCh:
			openWindow()
		case <-t.mSync.ClickedCh:
			go t.syncNow()
		case <-t.mQuit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

// alerting은 알림 대상 상태(로그인 필요, 표를 찾지 못함)만 고른다.
func alerting(states []core.TargetState) (login, empty []core.TargetState) {
	for _, st := range states {
		switch st.LastStatus {
		case core.StatusLoginRequired:
			login = append(login, st)
		case core.StatusEmpty:
			empty = append(empty, st)
		}
	}
	return login, empty
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
	t.mStatus.SetTitle(fmt.Sprintf("대상 %d개 · 로그 %d행 · 미전송 %d행", s.EnabledCount, s.Entries, s.Unsynced))
	t.mLast.SetTitle("마지막 수집: " + localTime(s.LastCapture))
	tip := fmt.Sprintf("ActivityLogKeeper — 미전송 %d행", s.Unsynced)

	login, empty := alerting(s.NeedAttention)
	if len(login)+len(empty) > 0 {
		title := "⚠"
		if len(login) > 0 {
			title += fmt.Sprintf(" 로그인 필요 %d개", len(login))
		}
		if len(empty) > 0 {
			title += fmt.Sprintf(" 표 못 찾음 %d개", len(empty))
		}
		t.mAttention.SetTitle(title)
		t.mAttention.Show()
		tip = "ActivityLogKeeper —" + title[len("⚠"):]
	} else {
		t.mAttention.Hide()
	}
	systray.SetTooltip(tip)
	t.notify(login, empty)
}

// notify는 새로 해당 상태가 된 대상만 알린다.
func (t *tray) notify(login, empty []core.TargetState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	current := map[int64]bool{}
	send := func(st core.TargetState, title, body string) {
		current[st.ID] = true
		if t.notified[st.ID] == st.LastStatus {
			return
		}
		t.notified[st.ID] = st.LastStatus
		name := st.Name
		if name == "" {
			name = st.URL
		}
		_ = beeep.Notify(title, name+"\n"+body, iconPNG)
	}
	for _, st := range login {
		send(st, "ActivityLogKeeper: 로그인 필요", "Chrome에서 다시 로그인해야 수집됩니다.")
	}
	for _, st := range empty {
		send(st, "ActivityLogKeeper: 표를 찾지 못함", "화면이 바뀌었는지 확인하고 관리 창에서 선택자를 지정하세요.")
	}
	// 다시 정상 수집되면 기록을 지워 다음에 또 알린다.
	for id := range t.notified {
		if !current[id] {
			delete(t.notified, id)
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
		_ = beeep.Notify("ActivityLogKeeper: 동기화 실패", err.Error(), iconPNG)
	case !res.APIConfigured:
		_ = beeep.Notify("ActivityLogKeeper", "Web API가 설정되지 않았습니다. 관리 창에서 설정하세요.", iconPNG)
	default:
		msg := fmt.Sprintf("보냄 %d행, 남음 %d행", res.Sent, res.Remaining)
		if res.Rejected > 0 {
			msg += fmt.Sprintf(", 서버 거부 %d행", res.Rejected)
		}
		_ = beeep.Notify("ActivityLogKeeper: 동기화 완료", msg, iconPNG)
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
