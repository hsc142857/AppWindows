//go:build !windows

package core

// Windows가 아닌 OS(개발용 Mac 등)에서는 암호화 없이 0600 파일 권한으로만 보호한다.
const secretProtectionAvailable = false

func protectSecret(plain string) (string, error) { return plain, nil }

func unprotectSecret(enc string) (string, error) { return enc, nil }
