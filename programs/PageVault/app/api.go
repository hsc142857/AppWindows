package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"appwindows/shared/apiclient"

	"pagevault/core"
)

// API는 관리 창 프런트엔드에 바인딩된다 (window.go.main.API.*).
type API struct {
	ctx   context.Context
	store *core.Store
}

type Overview struct {
	core.Status
	APIConfigured bool   `json:"apiConfigured"`
	HostInstalled bool   `json:"hostInstalled"`
	HostDetail    string `json:"hostDetail"`
	ExtensionID   string `json:"extensionID"`
}

func (a *API) Overview() (Overview, error) {
	s, err := a.store.Status()
	if err != nil {
		return Overview{}, err
	}
	o := Overview{Status: s, ExtensionID: core.DefaultExtensionID}
	o.API, o.APIConfigured = core.APISummary()
	o.HostInstalled, o.HostDetail = core.HostInstalled()
	return o, nil
}

// ---- 대상 ----

func (a *API) Targets() ([]core.TargetState, error) { return a.store.TargetStates() }

func (a *API) AddTarget(rawURL, name string) (core.Target, error) {
	return a.store.AddTarget(rawURL, name)
}

func (a *API) RemoveTarget(id int64) error { return a.store.RemoveTarget(id) }

func (a *API) SetTargetEnabled(id int64, enabled bool) error {
	return a.store.SetTargetEnabled(id, enabled)
}

func (a *API) RenameTarget(id int64, name string) error { return a.store.RenameTarget(id, name) }

// ---- 설정 ----

func (a *API) SaveSettings(intervalSeconds, loadDelaySeconds int) error {
	if err := core.ValidateInterval(intervalSeconds); err != nil {
		return err
	}
	if err := core.ValidateLoadDelay(loadDelaySeconds); err != nil {
		return err
	}
	if err := a.store.SetInterval(intervalSeconds); err != nil {
		return err
	}
	return a.store.SetLoadDelay(loadDelaySeconds)
}

// ---- 스냅샷 ----

func (a *API) Snapshots(limit int, targetURL string) ([]core.Snapshot, error) {
	return a.store.Snapshots(limit, targetURL)
}

type SnapshotDetail struct {
	core.Snapshot
	HTMLSize int `json:"htmlSize"`
}

// Snapshot은 본문 텍스트를 포함해 돌려준다. HTML은 크기만 알려 준다.
func (a *API) Snapshot(id int64) (SnapshotDetail, error) {
	s, err := a.store.Snapshot(id)
	if err != nil {
		return SnapshotDetail{}, err
	}
	d := SnapshotDetail{Snapshot: s, HTMLSize: len(s.HTML)}
	d.HTML = ""
	return d, nil
}

// ---- Web API ----

type APIView struct {
	Configured  bool   `json:"configured"`
	BaseURL     string `json:"baseURL"`
	MaskedToken string `json:"maskedToken"`
}

func (a *API) APIShow() (APIView, error) {
	c, ok, err := core.LoadAPIConfig()
	if err != nil || !ok {
		return APIView{}, err
	}
	return APIView{Configured: true, BaseURL: c.BaseURL, MaskedToken: c.MaskedToken()}, nil
}

// resolveAPI는 토큰 입력이 비어 있으면 저장된 토큰을 그대로 쓴다 (주소만 바꿀 때).
func resolveAPI(baseURL, token string) (apiclient.Config, error) {
	if strings.TrimSpace(token) == "" {
		old, ok, err := core.LoadAPIConfig()
		if err != nil {
			return apiclient.Config{}, err
		}
		if !ok {
			return apiclient.Config{}, errors.New("API 토큰을 입력하세요")
		}
		token = old.Token
	}
	return core.NewAPIConfig(baseURL, token)
}

func (a *API) APISave(baseURL, token string) (APIView, error) {
	c, err := resolveAPI(baseURL, token)
	if err != nil {
		return APIView{}, err
	}
	if err := core.SaveAPIConfig(c); err != nil {
		return APIView{}, err
	}
	return APIView{Configured: true, BaseURL: c.BaseURL, MaskedToken: c.MaskedToken()}, nil
}

// APITest는 입력값(저장 전)으로 접속과 토큰을 시험한다.
func (a *API) APITest(baseURL, token string) (string, error) {
	c, err := resolveAPI(baseURL, token)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	v, err := core.TestAPI(ctx, c)
	if err != nil {
		return "", err
	}
	if v == "" {
		v = "-"
	}
	return "접속 성공: " + c.BaseURL + " (서버 버전 " + v + ")", nil
}

func (a *API) APIClear() error { return core.ClearAPIConfig() }

// RetryRejected는 서버가 거부한 스냅샷을 다시 업로드 대상으로 돌린다.
func (a *API) RetryRejected() (int, error) { return a.store.ClearRejected() }

func (a *API) SyncNow() (core.SyncResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return core.SyncConfigured(ctx, a.store)
}

// ---- 호스트 등록 ----

// InstallHost는 PageVault.exe와 같은 폴더의 pagevault-cli.exe를 호스트로 등록한다.
func (a *API) InstallHost() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	src := filepath.Join(filepath.Dir(self), core.CLIExeName())
	res, err := core.InstallHost(src, nil)
	if err != nil {
		return "", err
	}
	return "등록 완료: " + res.Registered + "\nChrome 확장 프로그램을 다시 로드하세요.", nil
}

func (a *API) DataDir() string { return core.Home() }
