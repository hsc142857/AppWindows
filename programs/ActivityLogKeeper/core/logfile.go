package core

import (
	"io"
	"log"
	"os"
)

const maxLogSize = 5 << 20

// OpenLog는 host.log를 추가 모드로 연다. 5MB를 넘으면 host.log.1로 돌린다.
// 열 수 없으면 버리는 로거를 돌려준다 (stdout은 Native Messaging 전용이라 쓰면 안 된다).
func OpenLog(prefix string) (*log.Logger, io.Closer) {
	if err := EnsureHome(); err != nil {
		return log.New(io.Discard, "", 0), io.NopCloser(nil)
	}
	if fi, err := os.Stat(LogPath()); err == nil && fi.Size() > maxLogSize {
		_ = os.Rename(LogPath(), LogPath()+".1")
	}
	f, err := os.OpenFile(LogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return log.New(io.Discard, "", 0), io.NopCloser(nil)
	}
	return log.New(f, prefix, log.LstdFlags|log.Lmicroseconds), f
}
