package main

import (
	"embed"
	"os"
	goruntime "runtime"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

func init() {
	// WebKitGTK 2.42+ renders through DMA-BUF/GBM by default, which fails on
	// some NVIDIA/Mesa setups ("KMS: DRM_IOCTL_MODE_CREATE_DUMB failed:
	// Permission denied"). Fall back to the regular renderer unless the user
	// chose otherwise.
	if os.Getenv("WEBKIT_DISABLE_DMABUF_RENDERER") == "" {
		_ = os.Setenv("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
	}
}

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "PhantomOps",
		Width:     1180,
		Height:    820,
		MinWidth:  760,
		MinHeight: 520,
		// The app draws its own title bar (frontend/src/titlebar.js). On macOS
		// the native traffic-light buttons are kept instead, over a hidden
		// title bar, which is what Mac users expect.
		Frameless: goruntime.GOOS != "darwin",
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 14, G: 17, B: 24, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
		Linux: &linux.Options{
			Icon:             appIcon,
			ProgramName:      "phantomops",
			WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand,
		},
		Windows: &windows.Options{
			// Keep the system shadow, rounded corners and snap layouts.
			DisableFramelessWindowDecorations: false,
		},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
