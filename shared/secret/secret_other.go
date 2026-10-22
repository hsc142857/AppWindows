//go:build !windows

// Package secret은 API 키·토큰 같은 비밀값을 PC에 저장할 때 보호한다.
// Windows는 DPAPI(현재 사용자 범위)로 암호화하고, 그 밖의 OS(개발용)는 평문 그대로 둔다
// (그 경우 파일 권한 0600으로만 보호된다 — Available이 false).
package secret

const Available = false

func Protect(plain string) (string, error) { return plain, nil }

func Unprotect(enc string) (string, error) { return enc, nil }
