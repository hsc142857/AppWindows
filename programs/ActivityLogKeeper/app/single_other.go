//go:build !windows

package main

// 개발용 OS에서는 중복 실행을 막지 않는다.
func acquireSingleInstance(string) (release func(), ok bool) { return func() {}, true }
