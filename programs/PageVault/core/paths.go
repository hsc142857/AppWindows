// Package core는 PageVault의 저장소, 로그인 감지, Native Messaging, Web API 업로드를 담는다.
// CLI(cmd/pagevault)와 트레이 앱(app)이 함께 쓴다.
package core

import (
	"os"
	"path/filepath"
)

const (
	HostName           = "com.pagevault.host"
	DefaultExtensionID = "aojpddnbebpeglnlidlnhdkgpchgfdpe"
	Version            = "0.1.0"
)

// Home은 데이터 폴더를 돌려준다.
// PAGEVAULT_HOME이 있으면 그 값, Windows는 %LOCALAPPDATA%\PageVault, 그 밖에는 사용자 설정 폴더 아래 PageVault.
func Home() string {
	if h := os.Getenv("PAGEVAULT_HOME"); h != "" {
		return h
	}
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, "PageVault")
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "PageVault")
	}
	return filepath.Join(os.TempDir(), "PageVault")
}

func DBPath() string        { return filepath.Join(Home(), "pagevault.sqlite") }
func APIConfigPath() string { return filepath.Join(Home(), "api.json") }
func LogPath() string       { return filepath.Join(Home(), "host.log") }
func BinDir() string        { return filepath.Join(Home(), "bin") }

// legacyPostgresPath는 Web API로 바꾸기 전 Postgres 설정 파일이다 (안내용).
func legacyPostgresPath() string { return filepath.Join(Home(), "postgres.json") }

// EnsureHome은 데이터 폴더를 만든다 (사용자만 접근).
func EnsureHome() error {
	return os.MkdirAll(Home(), 0o700)
}
