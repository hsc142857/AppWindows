package core

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var extensionIDRe = regexp.MustCompile(`^[a-p]{32}$`)

type HostManifest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

type InstallResult struct {
	ExePath      string
	ManifestPath string
	Registered   string // 레지스트리 키(Windows) 또는 Chrome 매니페스트 위치
}

func hostExeName() string {
	if runtime.GOOS == "windows" {
		return "pagevault.exe"
	}
	return "pagevault"
}

// InstallHost는 호스트 실행 파일(srcExe, pagevault CLI)을 고정 위치로 복사하고
// Chrome에 Native Messaging 호스트로 등록한다.
func InstallHost(srcExe string, extensionIDs []string) (InstallResult, error) {
	if len(extensionIDs) == 0 {
		extensionIDs = []string{DefaultExtensionID}
	}
	origins := make([]string, 0, len(extensionIDs))
	for _, id := range extensionIDs {
		if !extensionIDRe.MatchString(id) {
			return InstallResult{}, errorf("확장 ID 형식이 아닙니다 (a~p 32자): %q", id)
		}
		origins = append(origins, "chrome-extension://"+id+"/")
	}
	if err := os.MkdirAll(BinDir(), 0o700); err != nil {
		return InstallResult{}, err
	}
	if _, err := os.Stat(srcExe); err != nil {
		return InstallResult{}, errorf("호스트 실행 파일을 찾을 수 없습니다: %w", err)
	}
	dst := filepath.Join(BinDir(), hostExeName())
	if err := copyExecutable(srcExe, dst); err != nil {
		return InstallResult{}, errorf("실행 파일 복사 실패: %w", err)
	}
	m := HostManifest{
		Name:           HostName,
		Description:    "PageVault native messaging host",
		Path:           dst,
		Type:           "stdio",
		AllowedOrigins: origins,
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	manifestPath := filepath.Join(Home(), HostName+".json")
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		return InstallResult{}, err
	}
	registered, err := registerHost(manifestPath, data)
	if err != nil {
		return InstallResult{}, err
	}
	return InstallResult{ExePath: dst, ManifestPath: manifestPath, Registered: registered}, nil
}

// CLIExeName은 배포 폴더에 들어 있는 CLI(호스트) 실행 파일 이름이다.
// GUI(PageVault.exe)와 대소문자만 다르면 Windows에서 같은 파일로 취급되므로 -cli를 붙인다.
// Chrome 호스트로 설치하면 bin\pagevault.exe로 복사된다 (별도 폴더라 충돌 없음).
func CLIExeName() string {
	if runtime.GOOS == "windows" {
		return "pagevault-cli.exe"
	}
	return "pagevault-cli"
}

// HostInstalled는 매니페스트·실행 파일이 고정 위치에 있고 Chrome에 등록되어 있는지 확인한다.
func HostInstalled() (bool, string) {
	manifestPath := filepath.Join(Home(), HostName+".json")
	for _, p := range []string{manifestPath, filepath.Join(BinDir(), hostExeName())} {
		if _, err := os.Stat(p); err != nil {
			return false, "없음: " + p
		}
	}
	return hostRegistered(manifestPath)
}

func UninstallHost() ([]string, error) {
	removed, err := unregisterHost()
	if err != nil {
		return removed, err
	}
	for _, p := range []string{filepath.Join(Home(), HostName+".json"), filepath.Join(BinDir(), hostExeName())} {
		if err := os.Remove(p); err == nil {
			removed = append(removed, p)
		}
	}
	return removed, nil
}

func copyExecutable(src, dst string) error {
	if a, err := filepath.EvalSymlinks(src); err == nil {
		src = a
	}
	if b, err := filepath.Abs(dst); err == nil && samePath(src, b) {
		return nil // 이미 고정 위치에서 실행 중
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// Windows는 실행 중인 exe를 덮어쓸 수 없지만 이름 변경은 가능하다.
	_ = os.Remove(dst + ".old")
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, dst+".old"); err != nil {
			return errorf("%s 이(가) 사용 중입니다. Chrome을 닫고 다시 시도하세요: %w", dst, err)
		}
	}
	return os.Rename(tmp, dst)
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
