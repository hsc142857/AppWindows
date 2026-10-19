//go:build windows

package core

import (
	"encoding/base64"
	"unsafe"

	"golang.org/x/sys/windows"
)

const secretProtectionAvailable = true

// protectSecret은 DPAPI(현재 사용자 범위)로 암호화해 base64로 돌려준다.
func protectSecret(plain string) (string, error) {
	in := []byte(plain)
	inBlob := windows.DataBlob{Size: uint32(len(in)), Data: &in[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&inBlob, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return base64.StdEncoding.EncodeToString(unsafe.Slice(out.Data, out.Size)), nil
}

func unprotectSecret(enc string) (string, error) {
	in, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(in) == 0 {
		return "", errorf("암호문 형식 오류")
	}
	inBlob := windows.DataBlob{Size: uint32(len(in)), Data: &in[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&inBlob, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return string(unsafe.Slice(out.Data, out.Size)), nil
}
