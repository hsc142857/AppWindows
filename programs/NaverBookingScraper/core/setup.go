package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"appwindows/shared/apiclient"
)

// SetupAPI는 인증키를 처음 설정하거나 바꿀 때 쓴다 (CLI api set, GUI 설정 화면 공통).
// key가 비어 있고 저장된 키가 있으면 그 키를 유지한다 (주소만 바꿀 때).
// 연결 테스트로 키를 확인한 뒤에만 저장하고, 키를 발급한 업체를 기억한다.
func SetupAPI(ctx context.Context, baseURL, key string) (apiclient.Config, Partner, PartnerChange, error) {
	if strings.TrimSpace(key) == "" {
		if old, err := LoadAPIConfig(); err == nil {
			key = old.Token
		}
	}
	c, err := NewAPIConfig(baseURL, key)
	if err != nil {
		return c, Partner{}, PartnerChange{}, err
	}
	ping, err := Ping(ctx, c)
	if err != nil {
		var apiErr *apiclient.APIError
		if errors.As(err, &apiErr) && (apiErr.Status == 401 || apiErr.Status == 403) {
			return c, Partner{}, PartnerChange{}, fmt.Errorf("인증키가 맞지 않습니다 — 저장하지 않았습니다 (%d)", apiErr.Status)
		}
		return c, Partner{}, PartnerChange{}, fmt.Errorf("서버 연결 실패 — 저장하지 않았습니다: %w", err)
	}
	if err := SaveAPIConfig(c); err != nil {
		return c, Partner{}, PartnerChange{}, err
	}
	p, change, err := RememberPartner(ping.PartnerID, ping.PartnerName)
	return c, p, change, err
}
