// PageVault.exe — 트레이 앱과 관리 창.
//
//	PageVault.exe            트레이(알림 영역)에 상주: 상태, 로그인 만료 알림, 지금 동기화
//	PageVault.exe --window   관리 창(Wails): 대상 URL·주기·Postgres 설정, 스냅샷 보기
//
// systray와 Wails는 각자 메시지 루프를 쓰므로 관리 창은 별도 프로세스로 띄운다.
// 두 프로세스와 Native Messaging 호스트는 같은 SQLite(WAL)를 함께 쓴다.
package main

import (
	"embed"
	"log"
	"os"
)

//go:embed all:frontend/dist
var frontendAssets embed.FS

//go:embed assets/icon.ico
var iconICO []byte

//go:embed assets/icon.png
var iconPNG []byte

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--window" {
		if err := runWindow(); err != nil {
			log.Fatal(err)
		}
		return
	}
	runTray()
}
