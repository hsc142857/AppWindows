package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"pagevault/core"
)

// API는 관리 창 프런트엔드에 바인딩된다 (window.go.main.API.*).
type API struct {
	ctx   context.Context
	store *core.Store
}

type Overview struct {
	core.Status
	PostgresConfigured bool   `json:"postgresConfigured"`
	HostInstalled      bool   `json:"hostInstalled"`
	HostDetail         string `json:"hostDetail"`
	ExtensionID        string `json:"extensionID"`
}

func (a *API) Overview() (Overview, error) {
	s, err := a.store.Status()
	if err != nil {
		return Overview{}, err
	}
	o := Overview{Status: s, ExtensionID: core.DefaultExtensionID}
	c, ok, err := core.LoadPGConfig()
	switch {
	case err != nil:
		o.Postgres = "설정 오류: " + err.Error()
	case ok:
		o.Postgres = c.Masked() + " (테이블 " + c.Table + ")"
		o.PostgresConfigured = true
	default:
		o.Postgres = "설정 안 됨"
	}
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

// ---- Postgres ----

type PGView struct {
	Configured bool   `json:"configured"`
	MaskedURL  string `json:"maskedURL"`
	Table      string `json:"table"`
}

func (a *API) PGShow() (PGView, error) {
	c, ok, err := core.LoadPGConfig()
	if err != nil || !ok {
		return PGView{Table: core.DefaultPGTable}, err
	}
	return PGView{Configured: true, MaskedURL: c.Masked(), Table: c.Table}, nil
}

// resolvePG는 입력 URL의 비밀번호가 ****이면 저장된 비밀번호로 바꿔 넣는다 (가린 값을 그대로 저장하지 않도록).
func resolvePG(rawURL, table string) (core.PGConfig, error) {
	rawURL = strings.TrimSpace(rawURL)
	if strings.Contains(rawURL, ":****@") {
		old, ok, err := core.LoadPGConfig()
		if err != nil {
			return core.PGConfig{}, err
		}
		if !ok {
			return core.PGConfig{}, errors.New("비밀번호를 입력하세요")
		}
		ou, err := url.Parse(old.URL)
		if err != nil {
			return core.PGConfig{}, err
		}
		pw, _ := ou.User.Password()
		rawURL = strings.Replace(rawURL, ":****@", ":"+url.UserPassword("x", pw).String()[2:]+"@", 1)
	}
	return core.NewPGConfig(rawURL, table)
}

func (a *API) PGSave(rawURL, table string) (PGView, error) {
	c, err := resolvePG(rawURL, table)
	if err != nil {
		return PGView{}, err
	}
	if err := core.SavePGConfig(c); err != nil {
		return PGView{}, err
	}
	return PGView{Configured: true, MaskedURL: c.Masked(), Table: c.Table}, nil
}

// PGTest는 입력값(저장 전)으로 접속을 시험한다.
func (a *API) PGTest(rawURL, table string) (string, error) {
	c, err := resolvePG(rawURL, table)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	v, err := core.TestPG(ctx, c)
	if err != nil {
		return "", err
	}
	return "접속 성공: PostgreSQL " + v + ", 테이블 " + c.Table + " 준비됨", nil
}

func (a *API) PGClear() error { return core.ClearPGConfig() }

func (a *API) SyncNow() (core.SyncResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return core.SyncConfigured(ctx, a.store)
}

// ---- 호스트 등록 ----

// InstallHost는 PageVault.exe와 같은 폴더의 pagevault.exe를 호스트로 등록한다.
func (a *API) InstallHost() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	src := filepath.Join(filepath.Dir(self), core.HostExeName())
	res, err := core.InstallHost(src, nil)
	if err != nil {
		return "", err
	}
	return "등록 완료: " + res.Registered + "\nChrome 확장 프로그램을 다시 로드하세요.", nil
}

func (a *API) DataDir() string { return core.Home() }
