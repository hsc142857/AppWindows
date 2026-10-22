package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"appwindows/shared/apiclient"
	"appwindows/shared/secret"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"naverbookingscraper/core"
)

// App은 프런트엔드에 바인딩된다 (window.go.main.App.*). 데이터 폴더·인증키는 CLI와 같은 것을 쓴다.
type App struct {
	ctx   context.Context
	store *core.Store
}

func timeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// State는 네트워크 없이 저장된 설정을 돌려준다 (처음 화면을 고를 때 쓴다).
type State struct {
	Configured     bool         `json:"configured"`
	BaseURL        string       `json:"baseURL"`
	MaskedKey      string       `json:"maskedKey"`
	Partner        core.Partner `json:"partner"`
	DefaultBaseURL string       `json:"defaultBaseURL"`
	Home           string       `json:"home"`
	Encrypted      bool         `json:"encrypted"` // 키를 OS 암호화(Windows DPAPI)로 보관하는지
	Version        string       `json:"version"`
}

func (a *App) State() (State, error) {
	st := State{DefaultBaseURL: core.DefaultBaseURL, Home: core.Home(), Encrypted: secret.Available, Version: core.Version}
	c, err := core.LoadAPIConfig()
	switch {
	case errors.Is(err, core.ErrNoAPIKey):
		return st, nil
	case err != nil:
		return st, err
	}
	st.Configured, st.BaseURL, st.MaskedKey = true, c.BaseURL, c.MaskedToken()
	st.Partner, _, _ = core.LoadPartner()
	return st, nil
}

// Connection은 서버에 물어 확인한 결과다.
type Connection struct {
	BaseURL         string       `json:"baseURL"`
	MaskedKey       string       `json:"maskedKey"`
	Partner         core.Partner `json:"partner"`
	PartnerChanged  bool         `json:"partnerChanged"`
	PreviousPartner string       `json:"previousPartner"`
}

func connection(c apiclient.Config, p core.Partner, ch core.PartnerChange) Connection {
	return Connection{BaseURL: c.BaseURL, MaskedKey: c.MaskedToken(), Partner: p,
		PartnerChanged: ch.Changed, PreviousPartner: ch.Previous.Label()}
}

// Connect는 실행할 때 부른다: 저장된 키로 서버에 연결해 인증키를 발급한 업체를 확인한다.
func (a *App) Connect() (Connection, error) {
	c, err := core.LoadAPIConfig()
	if err != nil {
		return Connection{}, err
	}
	ctx, cancel := timeout(30 * time.Second)
	defer cancel()
	p, ch, err := core.CheckPartner(ctx, c)
	if err != nil {
		return Connection{}, err
	}
	return connection(c, p, ch), nil
}

// SaveKey는 인증키를 확인한 뒤 저장한다. key를 비우면 저장된 키를 유지한다 (주소만 바꿀 때).
func (a *App) SaveKey(baseURL, key string) (Connection, error) {
	ctx, cancel := timeout(30 * time.Second)
	defer cancel()
	c, p, ch, err := core.SetupAPI(ctx, baseURL, key)
	if err != nil {
		return Connection{}, err
	}
	return connection(c, p, ch), nil
}

func (a *App) ClearKey() error { return core.ClearAPIConfig() }

func (a *App) Today() string { return time.Now().In(core.Seoul).Format("2006-01-02") }

// ListDay는 서버에 저장된 그날 예약을 돌려준다.
func (a *App) ListDay(date string) (core.ListResult, error) {
	c, err := core.LoadAPIConfig()
	if err != nil {
		return core.ListResult{}, err
	}
	ctx, cancel := timeout(30 * time.Second)
	defer cancel()
	res, err := core.ListDay(ctx, c, date)
	if err == nil && res.PartnerID != "" {
		core.RememberPartner(res.PartnerID, res.PartnerName)
	}
	return res, err
}

type FindResult struct {
	Found  bool           `json:"found"`
	Result core.OneResult `json:"result"`
}

func (a *App) FindVisit(id string) (FindResult, error) {
	c, err := core.LoadAPIConfig()
	if err != nil {
		return FindResult{}, err
	}
	ctx, cancel := timeout(30 * time.Second)
	defer cancel()
	res, found, err := core.GetVisit(ctx, c, id)
	return FindResult{Found: found, Result: res}, err
}

// PickFile은 예약 JSON 파일을 고르는 대화상자를 띄운다. 취소하면 빈 문자열.
func (a *App) PickFile() (string, error) {
	return wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title:   "예약 JSON 파일 선택",
		Filters: []wailsruntime.FileFilter{{DisplayName: "예약 JSON (*.json)", Pattern: "*.json"}},
	})
}

type UploadResult struct {
	File    string            `json:"file"`
	Count   int               `json:"count"`
	Partner core.Partner      `json:"partner"`
	Result  core.ImportResult `json:"result"`
}

func readReservations(path string) ([]core.Reservation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var list []core.Reservation
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("JSON 형식 오류 (Reservation 배열이어야 합니다): %w", err)
	}
	return list, nil
}

// Preview는 예약 JSON을 서버에 쓰지 않고(dryRun) 예상 결과만 받는다. 로컬에도 저장하지 않는다.
func (a *App) Preview(path string) (UploadResult, error) {
	list, err := readReservations(path)
	if err != nil {
		return UploadResult{}, err
	}
	c, err := core.LoadAPIConfig()
	if err != nil {
		return UploadResult{}, err
	}
	ctx, cancel := timeout(2 * time.Minute)
	defer cancel()
	p, _, err := core.CheckPartner(ctx, c)
	if err != nil {
		return UploadResult{}, err
	}
	items := make([]core.ImportItem, len(list))
	for i, r := range list {
		items[i] = r.ToImportItem()
	}
	res, err := core.Import(ctx, c, items, true)
	return UploadResult{File: filepath.Base(path), Count: len(list), Partner: p, Result: res}, err
}

// currentPartner는 지금 인증키의 업체다. 서버에 물어보고, 서버가 안 되면 마지막으로 확인한 업체를 쓴다.
func currentPartner(ctx context.Context, c apiclient.Config) (core.Partner, error) {
	p, _, err := core.CheckPartner(ctx, c)
	if err == nil {
		return p, nil
	}
	if saved, ok, _ := core.LoadPartner(); ok {
		return saved, nil
	}
	return core.Partner{}, fmt.Errorf("인증키의 업체를 확인하지 못했습니다: %w", err)
}

type CollectResult struct {
	File    string             `json:"file"`
	Partner core.Partner       `json:"partner"`
	Saved   core.SaveSummary   `json:"saved"`
	Upload  core.UploadSummary `json:"upload"`
}

// Collect는 긁어 온 예약을 저장하고 바로 전송한다 (Chrome 확장 수집이 붙기 전에는 JSON 파일로 대신한다).
// 전송이 실패해도 예약은 저장되어 "미전송"에 나오고, 다음 전송 때 다시 보낸다.
func (a *App) Collect(path string) (CollectResult, error) {
	list, err := readReservations(path)
	if err != nil {
		return CollectResult{}, err
	}
	c, err := core.LoadAPIConfig()
	if err != nil {
		return CollectResult{}, err
	}
	ctx, cancel := timeout(2 * time.Minute)
	defer cancel()
	p, err := currentPartner(ctx, c)
	if err != nil {
		return CollectResult{}, err
	}
	saved, err := a.store.SaveScraped(p.ID, list)
	if err != nil {
		return CollectResult{}, err
	}
	up, err := a.store.UploadPending(ctx, c, p.ID)
	return CollectResult{File: filepath.Base(path), Partner: p, Saved: saved, Upload: up}, err
}

// SendNow는 미전송(대기·전송 실패)을 지금 보낸다. GUI가 1분마다 자동으로도 부른다.
func (a *App) SendNow() (core.UploadSummary, error) {
	c, err := core.LoadAPIConfig()
	if err != nil {
		return core.UploadSummary{}, err
	}
	ctx, cancel := timeout(2 * time.Minute)
	defer cancel()
	p, err := currentPartner(ctx, c)
	if err != nil {
		return core.UploadSummary{}, err
	}
	return a.store.UploadPending(ctx, c, p.ID)
}

// RetryRejected는 거부된 예약을 다시 전송 대상으로 돌리고 바로 보낸다.
func (a *App) RetryRejected() (core.UploadSummary, error) {
	p, ok, _ := core.LoadPartner()
	if !ok {
		return core.UploadSummary{}, errors.New("업체가 확인되지 않았습니다")
	}
	if _, err := a.store.RetryRejected(p.ID); err != nil {
		return core.UploadSummary{}, err
	}
	return a.SendNow()
}

type Records struct {
	Partner core.Partner            `json:"partner"`
	Counts  core.Counts             `json:"counts"`
	Unsent  []core.LocalReservation `json:"unsent"` // 대기 · 전송 실패 · 거부 · 다른 업체
	Sent    []core.LocalReservation `json:"sent"`   // 서버가 처리한 것만
}

// Records는 로컬에 저장된 예약을 전송 완료 / 미전송으로 나눠 돌려준다 (네트워크 없음).
func (a *App) Records() (Records, error) {
	p, _, _ := core.LoadPartner()
	list, counts, err := a.store.List(p.ID)
	if err != nil {
		return Records{}, err
	}
	r := Records{Partner: p, Counts: counts, Unsent: []core.LocalReservation{}, Sent: []core.LocalReservation{}}
	for _, lr := range list {
		if lr.State == core.StateSent {
			r.Sent = append(r.Sent, lr)
		} else {
			r.Unsent = append(r.Unsent, lr)
		}
	}
	return r, nil
}
