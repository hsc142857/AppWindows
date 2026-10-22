// NaverBookingScraper.exe / NaverBookingScraper.app — 관리 창 (Wails).
//
// 지금 되는 것: 인증키 설정(처음 한 번), 업체 확인, 전송 현황(전송 완료 / 미전송),
// 서버 예약 조회, 예약번호 찾기, 예약 JSON 가져오기(수집 대신 — 저장 후 바로 전송).
// GUI의 "전송 완료"에는 서버가 처리했다고 응답한 예약만 나온다.
// 예정: 트레이 상주, Chrome 확장 수집 결과 보기, 업체(파트너센터) 목록 관리.
package main

import (
	"context"
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"naverbookingscraper/core"
)

//go:embed all:frontend/dist
var frontendAssets embed.FS

func main() {
	store, err := core.OpenStore()
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	app := &App{store: store}
	err = wails.Run(&options.App{
		Title:     "NaverBookingScraper",
		Width:     1040,
		Height:    720,
		MinWidth:  760,
		MinHeight: 520,
		AssetServer: &assetserver.Options{
			Assets: frontendAssets,
		},
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 255},
		OnStartup:        func(ctx context.Context) { app.ctx = ctx },
		Bind:             []any{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "com.appwindows.naverbookingscraper",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				if app.ctx != nil {
					wailsruntime.WindowUnminimise(app.ctx)
					wailsruntime.Show(app.ctx)
				}
			},
		},
		Windows: &windows.Options{Theme: windows.SystemDefault},
		Mac:     &mac.Options{About: &mac.AboutInfo{Title: "NaverBookingScraper", Message: "네이버예약 스크랩"}},
	})
	if err != nil {
		log.Fatal(err)
	}
}
