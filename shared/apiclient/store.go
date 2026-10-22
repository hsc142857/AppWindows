package apiclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"appwindows/shared/secret"
)

// configFile은 각 프로그램 데이터 폴더의 api.json 형식이다.
// 토큰(인증키)은 사용자가 한 번만 입력하면 여기에 저장되고, Windows에서는 DPAPI로 암호화된다.
type configFile struct {
	BaseURL    string `json:"baseURL"`
	TokenDPAPI string `json:"token_dpapi,omitempty"` // Windows: DPAPI(현재 사용자)
	Token      string `json:"token,omitempty"`       // 그 밖의 OS(개발용): 파일 권한 0600으로만 보호
}

// LoadConfig는 path의 설정을 읽는다. 파일이 없으면 ok=false.
func LoadConfig(path string) (cfg Config, ok bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, err
	}
	var f configFile
	if err := json.Unmarshal(data, &f); err != nil {
		return Config{}, false, fmt.Errorf("%s을(를) 읽을 수 없습니다: %w", filepath.Base(path), err)
	}
	token := f.Token
	if f.TokenDPAPI != "" {
		if token, err = secret.Unprotect(f.TokenDPAPI); err != nil {
			return Config{}, false, fmt.Errorf("저장된 인증키를 복호화할 수 없습니다 (다른 사용자·PC에서 저장했나요?): %w", err)
		}
	}
	return Config{BaseURL: f.BaseURL, Token: token}, true, nil
}

// SaveConfig는 설정을 검증해 path에 저장한다 (0600, 임시 파일 후 교체).
func SaveConfig(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f := configFile{BaseURL: c.BaseURL}
	if secret.Available {
		enc, err := secret.Protect(c.Token)
		if err != nil {
			return fmt.Errorf("인증키 암호화 실패: %w", err)
		}
		f.TokenDPAPI = enc
	} else {
		f.Token = c.Token
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ClearConfig는 저장된 설정을 지운다. 없어도 오류가 아니다.
func ClearConfig(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
