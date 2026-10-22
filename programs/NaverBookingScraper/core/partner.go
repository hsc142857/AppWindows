package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"appwindows/shared/apiclient"
)

// Partner는 인증키를 발급한 업체다. 여러 업체가 같은 프로그램을 쓰므로
// 실행할 때 서버에 물어 확인하고, 마지막으로 확인한 값을 partner.json에 기억한다 (비밀값 아님).
type Partner struct {
	ID        string `json:"partnerId"`
	Name      string `json:"partnerName"`
	CheckedAt string `json:"checkedAt"` // 마지막 확인 시각 (UTC, RFC3339)
}

// Label은 화면 표시용 업체 이름이다 (이름이 없으면 ID).
func (p Partner) Label() string {
	switch {
	case p.Name != "":
		return p.Name
	case p.ID != "":
		return "업체 " + p.ID
	}
	return "업체 미확인"
}

// PartnerChange는 기억해 둔 업체와 지금 키의 업체가 다를 때 채워진다.
type PartnerChange struct {
	Changed  bool
	Previous Partner
}

func PartnerPath() string { return filepath.Join(Home(), "partner.json") }

// LoadPartner는 마지막으로 확인한 업체를 읽는다. 없으면 ok=false.
func LoadPartner() (Partner, bool, error) {
	data, err := os.ReadFile(PartnerPath())
	if errors.Is(err, os.ErrNotExist) {
		return Partner{}, false, nil
	}
	if err != nil {
		return Partner{}, false, err
	}
	var p Partner
	if err := json.Unmarshal(data, &p); err != nil {
		return Partner{}, false, err
	}
	return p, p.ID != "", nil
}

// RememberPartner는 서버 응답에서 받은 업체를 기억하고, 이전 업체와 다르면 알려 준다.
// 업체 ID가 비어 있으면(이전 서버) 아무것도 하지 않는다.
func RememberPartner(id, name string) (Partner, PartnerChange, error) {
	p := Partner{ID: id, Name: name, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	if id == "" {
		return p, PartnerChange{}, nil
	}
	prev, ok, err := LoadPartner()
	if err != nil {
		prev, ok = Partner{}, false // 깨진 파일은 덮어쓴다
	}
	change := PartnerChange{Changed: ok && prev.ID != id, Previous: prev}
	if err := os.MkdirAll(Home(), 0o700); err != nil {
		return p, change, err
	}
	data, _ := json.MarshalIndent(p, "", "  ")
	tmp := PartnerPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return p, change, err
	}
	return p, change, os.Rename(tmp, PartnerPath())
}

func ClearPartner() error {
	err := os.Remove(PartnerPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// CheckPartner는 실행할 때 부른다: 연결 테스트로 인증키를 확인하고 발급 업체를 받아 기억한다.
// 트레이 앱은 시작할 때, CLI는 업로드 전에 쓴다.
func CheckPartner(ctx context.Context, c apiclient.Config) (Partner, PartnerChange, error) {
	ping, err := Ping(ctx, c)
	if err != nil {
		return Partner{}, PartnerChange{}, err
	}
	return RememberPartner(ping.PartnerID, ping.PartnerName)
}
