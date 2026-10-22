// Package core는 ActivityLogKeeper의 저장소(활동 로그 행), 로그인 감지, Native Messaging, Web API 업로드를 담는다.
// CLI(cmd/activitylogkeeper)가 쓰고, 이후 트레이·관리 창(app)도 같은 코어를 쓴다.
package core

import (
	"os"
	"path/filepath"
)

const (
	HostName           = "com.appwindows.activitylogkeeper"
	DefaultExtensionID = "bokkckkphfafbndhkgncoledplffljih" // extension/manifest.json의 key로 고정
	Version            = "0.1.0"
)

// Home은 데이터 폴더를 돌려준다.
// ACTIVITYLOGKEEPER_HOME이 있으면 그 값, Windows는 %LOCALAPPDATA%\ActivityLogKeeper, 그 밖에는 사용자 설정 폴더 아래.
func Home() string {
	if h := os.Getenv("ACTIVITYLOGKEEPER_HOME"); h != "" {
		return h
	}
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, "ActivityLogKeeper")
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "ActivityLogKeeper")
	}
	return filepath.Join(os.TempDir(), "ActivityLogKeeper")
}

func DBPath() string        { return filepath.Join(Home(), "activitylog.sqlite") }
func APIConfigPath() string { return filepath.Join(Home(), "api.json") }
func LogPath() string       { return filepath.Join(Home(), "host.log") }
func BinDir() string        { return filepath.Join(Home(), "bin") }

// EnsureHome은 데이터 폴더를 만든다 (사용자만 접근).
func EnsureHome() error {
	return os.MkdirAll(Home(), 0o700)
}
