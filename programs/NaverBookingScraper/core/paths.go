package core

import (
	"os"
	"path/filepath"
)

const (
	AppName            = "NaverBookingScraper"
	Version            = "0.1.0"
	HostName           = "com.appwindows.naverbookingscraper"
	DefaultExtensionID = "dabccgindpajbmnhcbfemdnngddecmdh" // extension/manifest.json의 key로 고정

	// DefaultBaseURL은 Visit Import API 기본 주소다 (개발 서버). 운영 주소는 api set으로 바꾼다.
	DefaultBaseURL = "http://localhost:3000"
)

// Home은 데이터 폴더다. NAVERBOOKINGSCRAPER_HOME > %LOCALAPPDATA%\NaverBookingScraper > 사용자 설정 폴더.
func Home() string {
	if h := os.Getenv("NAVERBOOKINGSCRAPER_HOME"); h != "" {
		return h
	}
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, AppName)
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, AppName)
	}
	return filepath.Join(os.TempDir(), AppName)
}

// EnsureHome은 데이터 폴더를 만든다 (사용자만 접근).
func EnsureHome() error { return os.MkdirAll(Home(), 0o700) }

func DBPath() string     { return filepath.Join(Home(), "naverbookingscraper.sqlite") }
func ConfigPath() string { return filepath.Join(Home(), "api.json") }
func LogPath() string    { return filepath.Join(Home(), "host.log") }
