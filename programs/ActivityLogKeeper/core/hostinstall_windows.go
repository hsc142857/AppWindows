//go:build windows

package core

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const hostRegistryKey = `Software\Google\Chrome\NativeMessagingHosts\` + HostName

// registerHost는 HKCU 레지스트리 키 기본값에 매니페스트 경로를 등록한다.
func registerHost(manifestPath string, _ []byte) (string, error) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, hostRegistryKey, registry.SET_VALUE)
	if err != nil {
		return "", errorf("레지스트리 키 생성 실패: %w", err)
	}
	defer k.Close()
	if err := k.SetStringValue("", manifestPath); err != nil {
		return "", errorf("레지스트리 값 설정 실패: %w", err)
	}
	return `HKCU\` + hostRegistryKey, nil
}

func unregisterHost() ([]string, error) {
	err := registry.DeleteKey(registry.CURRENT_USER, hostRegistryKey)
	if errors.Is(err, registry.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errorf("레지스트리 키 삭제 실패: %w", err)
	}
	return []string{`HKCU\` + hostRegistryKey}, nil
}

func hostRegistered(manifestPath string) (bool, string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, hostRegistryKey, registry.QUERY_VALUE)
	if err != nil {
		return false, "레지스트리에 등록되지 않음"
	}
	defer k.Close()
	v, _, err := k.GetStringValue("")
	if err != nil || !strings.EqualFold(v, manifestPath) {
		return false, "레지스트리 값이 다름: " + v
	}
	return true, `HKCU\` + hostRegistryKey
}
