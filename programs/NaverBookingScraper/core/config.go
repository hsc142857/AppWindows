package core

import (
	"errors"
	"strings"

	"appwindows/shared/apiclient"
)

// ErrNoAPIKey는 인증키를 아직 입력하지 않았을 때 돌려준다.
var ErrNoAPIKey = errors.New("인증키가 설정되지 않았습니다 (naverbookingscraper-cli api set 또는 관리 창에서 한 번 입력하세요)")

// NewAPIConfig는 주소(비우면 기본 주소)와 인증키로 설정을 만든다.
func NewAPIConfig(baseURL, key string) (apiclient.Config, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return apiclient.Config{}, errors.New("인증키를 입력하세요 (Motospec 탑바 «인증키»에서 발급한 msvi_… 키)")
	}
	if !strings.HasPrefix(key, "msvi_") {
		return apiclient.Config{}, errors.New("인증키는 msvi_ 로 시작해야 합니다 (Motospec 탑바 «인증키»에서 발급)")
	}
	c := apiclient.Config{BaseURL: baseURL, Token: key}
	return c, c.Validate()
}

// LoadAPIConfig는 저장된 설정을 읽는다. 없으면 ErrNoAPIKey.
func LoadAPIConfig() (apiclient.Config, error) {
	c, ok, err := apiclient.LoadConfig(ConfigPath())
	if err != nil {
		return c, err
	}
	if !ok {
		return c, ErrNoAPIKey
	}
	return c, nil
}

// SaveAPIConfig는 인증키를 저장한다. Windows에서는 DPAPI로 암호화되어 같은 사용자만 읽을 수 있다.
func SaveAPIConfig(c apiclient.Config) error { return apiclient.SaveConfig(ConfigPath(), c) }

// ClearAPIConfig는 인증키와 기억해 둔 업체 정보를 함께 지운다.
func ClearAPIConfig() error {
	if err := apiclient.ClearConfig(ConfigPath()); err != nil {
		return err
	}
	return ClearPartner()
}

func newClient(c apiclient.Config) (*apiclient.Client, error) {
	return apiclient.New(c, AppName+"/"+Version)
}
