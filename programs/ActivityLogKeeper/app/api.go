package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"appwindows/shared/apiclient"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"activitylogkeeper/core"
)

// API는 관리 창 프런트엔드에 바인딩된다 (window.go.main.API.*).
type API struct {
	ctx   context.Context
	store *core.Store
}

type Overview struct {
	core.Status
	API           string `json:"api"`
	APIConfigured bool   `json:"apiConfigured"`
	HostInstalled bool   `json:"hostInstalled"`
	HostDetail    string `json:"hostDetail"`
	ExtensionID   string `json:"extensionID"`
	Version       string `json:"version"`
}

func (a *API) Overview() (Overview, error) {
	s, err := a.store.Status()
	if err != nil {
		return Overview{}, err
	}
	o := Overview{Status: s, ExtensionID: core.DefaultExtensionID, Version: core.Version}
	o.API, o.APIConfigured = core.APISummary()
	o.HostInstalled, o.HostDetail = core.HostInstalled()
	return o, nil
}

// ---- 대상 ----

func (a *API) Targets() ([]core.TargetState, error) { return a.store.TargetStates() }

func (a *API) AddTarget(rawURL, name, selector string) (core.Target, error) {
	return a.store.AddTarget(rawURL, name, selector)
}

// UpdateTarget은 이름과 선택자를 함께 바꾼다 (선택자를 비우면 자동: 행이 가장 많은 표).
func (a *API) UpdateTarget(id int64, name, selector string) error {
	if err := a.store.RenameTarget(id, name); err != nil {
		return err
	}
	return a.store.SetTargetSelector(id, selector)
}

func (a *API) RemoveTarget(id int64) error { return a.store.RemoveTarget(id) }

func (a *API) SetTargetEnabled(id int64, enabled bool) error {
	return a.store.SetTargetEnabled(id, enabled)
}

// ---- 로그 ----

// entryFilter는 화면의 날짜(YYYY-MM-DD, PC 시간대)를 저장 형식 조건으로 바꾼다. to 날짜도 포함한다.
func entryFilter(targetURL, from, to string) (core.EntryFilter, error) {
	f := core.EntryFilter{TargetURL: targetURL}
	var err error
	if f.From, err = dayStart(from, 0); err != nil {
		return f, err
	}
	if f.To, err = dayStart(to, 1); err != nil {
		return f, err
	}
	return f, nil
}

func dayStart(s string, addDays int) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	d, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return "", fmt.Errorf("날짜는 YYYY-MM-DD 형식이어야 합니다: %q", s)
	}
	return core.FormatTime(d.AddDate(0, 0, addDays)), nil
}

// Entries는 조건에 맞는 로그를 최근 것부터 limit개 돌려준다.
func (a *API) Entries(targetURL, from, to string, limit int) ([]core.Entry, error) {
	f, err := entryFilter(targetURL, from, to)
	if err != nil {
		return nil, err
	}
	f.Limit, f.Newest = limit, true
	return a.store.Entries(f)
}

// ExportCSV는 저장 위치를 물은 뒤 조건에 맞는 로그 전체를 CSV로 쓴다. 취소하면 빈 문자열.
func (a *API) ExportCSV(targetURL, from, to string) (string, error) {
	f, err := entryFilter(targetURL, from, to)
	if err != nil {
		return "", err
	}
	list, err := a.store.Entries(f)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "", errors.New("조건에 맞는 로그가 없습니다")
	}
	path, err := wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
		Title:           "CSV로 내보내기",
		DefaultFilename: "activitylog-" + time.Now().Format("20060102-1504") + ".csv",
		Filters:         []wailsruntime.FileFilter{{DisplayName: "CSV (*.csv)", Pattern: "*.csv"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	if filepath.Ext(path) == "" {
		path += ".csv"
	}
	file, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if err := core.WriteCSV(file, list, time.Local); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d행을 저장했습니다: %s", len(list), path), nil
}

// ---- 수집 기록 ----

func (a *API) Captures(limit int, targetURL string) ([]core.CaptureRecord, error) {
	return a.store.Captures(limit, targetURL)
}

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

// RetryRejected는 서버가 거부한 행을 다시 업로드 대상으로 돌린다.
func (a *API) RetryRejected() (int, error) { return a.store.ClearRejected() }

func (a *API) SyncNow() (core.SyncResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return core.SyncConfigured(ctx, a.store)
}

// ---- 호스트 등록 ----

// InstallHost는 ActivityLogKeeper.exe와 같은 폴더의 activitylogkeeper-cli.exe를 호스트로 등록한다.
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
