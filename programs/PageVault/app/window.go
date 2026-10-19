package main

import (
	"context"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"pagevault/core"
)

func runWindow() error {
	st, err := core.OpenStore()
	if err != nil {
		return err
	}
	defer st.Close()
	api := &API{store: st}

	return wails.Run(&options.App{
		Title:     "PageVault",
		Width:     1040,
		Height:    720,
		MinWidth:  760,
		MinHeight: 520,
		AssetServer: &assetserver.Options{
			Assets: frontendAssets,
		},
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 255},
		OnStartup:        func(ctx context.Context) { api.ctx = ctx },
		Bind:             []any{api},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "com.pagevault.window",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				if api.ctx != nil {
					wailsruntime.WindowUnminimise(api.ctx)
					wailsruntime.Show(api.ctx)
				}
			},
		},
		Windows: &windows.Options{
			Theme: windows.SystemDefault,
		},
	})
}
