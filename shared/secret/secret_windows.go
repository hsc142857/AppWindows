//go:build windows

// Package secret은 API 키·토큰 같은 비밀값을 PC에 저장할 때 보호한다.
// Windows는 DPAPI(현재 사용자 범위)로 암호화하고, 그 밖의 OS(개발용)는 평문 그대로 둔다
// (그 경우 파일 권한 0600으로만 보호된다 — Available이 false).
package secret

import (
	"encoding/base64"
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Available은 암호화 보호를 쓸 수 있는지 알려 준다.
const Available = true

// Protect는 DPAPI로 암호화해 base64로 돌려준다. 같은 Windows 사용자만 복호화할 수 있다.
func Protect(plain string) (string, error) {
	if plain == "" {
		return "", errors.New("빈 값은 암호화할 수 없습니다")
	}
	in := []byte(plain)
	inBlob := windows.DataBlob{Size: uint32(len(in)), Data: &in[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&inBlob, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return base64.StdEncoding.EncodeToString(unsafe.Slice(out.Data, out.Size)), nil
}

func Unprotect(enc string) (string, error) {
	in, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(in) == 0 {
		return "", errors.New("암호문 형식 오류")
	}
	inBlob := windows.DataBlob{Size: uint32(len(in)), Data: &in[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&inBlob, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return string(unsafe.Slice(out.Data, out.Size)), nil
}
