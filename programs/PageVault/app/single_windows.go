//go:build windows

package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

// acquireSingleInstance는 이름 있는 뮤텍스로 같은 사용자 세션의 중복 실행을 막는다.
func acquireSingleInstance(name string) (release func(), ok bool) {
	p, err := windows.UTF16PtrFromString(`Local\` + name)
	if err != nil {
		return func() {}, true
	}
	h, err := windows.CreateMutex(nil, false, p)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, false
	}
	if err != nil {
		return func() {}, true
	}
	return func() { windows.CloseHandle(h) }, true
}
