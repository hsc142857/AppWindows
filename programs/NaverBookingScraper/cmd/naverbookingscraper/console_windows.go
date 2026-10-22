//go:build windows

package main

import "golang.org/x/sys/windows"

// 오래된 cmd.exe는 코드 페이지 949라 UTF-8 한글 출력이 깨진다. 시작할 때 콘솔을 UTF-8로 맞춘다.
// (콘솔이 없으면 — Chrome이 호스트로 실행할 때 — 오류를 무시한다.)
func init() {
	_ = windows.SetConsoleOutputCP(65001)
	_ = windows.SetConsoleCP(65001)
}
