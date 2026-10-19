//go:build !windows

package core

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// Mac·Linux(개발용)는 Chrome 사용자 폴더의 NativeMessagingHosts에 매니페스트를 둔다.
func chromeHostManifestPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "google-chrome", "NativeMessagingHosts")
	if runtime.GOOS == "darwin" {
		dir = filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "NativeMessagingHosts")
	}
	return filepath.Join(dir, HostName+".json"), nil
}

func registerHost(_ string, manifest []byte) (string, error) {
	p, err := chromeHostManifestPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	return p, os.WriteFile(p, manifest, 0o644)
}

func unregisterHost() ([]string, error) {
	p, err := chromeHostManifestPath()
	if err != nil {
		return nil, err
	}
	if err := os.Remove(p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return []string{p}, nil
}

func hostRegistered(string) (bool, string) {
	p, err := chromeHostManifestPath()
	if err != nil {
		return false, err.Error()
	}
	if _, err := os.Stat(p); err != nil {
		return false, "Chrome 매니페스트 없음: " + p
	}
	return true, p
}
