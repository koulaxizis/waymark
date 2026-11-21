// Waymark Remote: one portable app that runs as Agent (the presenter laptop)
// or as Controller (operator, secretariat or supervisor). No installer, no
// admin rights; settings and the default Agent folder live next to the exe.
package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

// Version is set at build time with -ldflags "-X main.Version=...".
var Version = "0.1.0"

//go:embed all:frontend
var assets embed.FS

func main() {
	app := NewApp()
	front, err := fs.Sub(assets, "frontend")
	if err != nil {
		log.Fatal(err)
	}
	err = wails.Run(&options.App{
		Title:            "Waymark Remote",
		Width:            1100,
		Height:           760,
		MinWidth:         380,
		MinHeight:        560,
		BackgroundColour: &options.RGBA{R: 9, G: 14, B: 14, A: 255},
		AssetServer: &assetserver.Options{
			Assets:  front,
			Handler: http.HandlerFunc(app.serveFile),
		},
		DragAndDrop: &options.DragAndDrop{EnableFileDrop: true},
		OnStartup:   app.startup,
		OnBeforeClose: func(ctx context.Context) bool {
			return app.beforeClose()
		},
		OnShutdown: func(ctx context.Context) { app.shutdown() },
		Bind:       []any{app},
		Windows: &windows.Options{
			WebviewUserDataPath: webviewDataPath(),
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// exeDir is the folder of the executable: the portable home of Waymark.
func exeDir() string {
	p, err := os.Executable()
	if err != nil {
		return "."
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	d := filepath.Dir(p)
	// An AppImage mounts read-only; keep data next to the AppImage file.
	if ai := os.Getenv("APPIMAGE"); ai != "" {
		d = filepath.Dir(ai)
	}
	// A macOS .app keeps data next to the bundle.
	if i := strings.Index(d, ".app/Contents/MacOS"); i > 0 {
		d = filepath.Dir(d[:i+4])
	}
	return d
}

// dataDir is where settings go: next to the exe when writable, else the
// user's config folder.
func dataDir() string {
	d := exeDir()
	probe := filepath.Join(d, ".waymark-write-test")
	if f, err := os.Create(probe); err == nil {
		f.Close()
		os.Remove(probe)
		return d
	}
	if c, err := os.UserConfigDir(); err == nil {
		d = filepath.Join(c, "Waymark")
		os.MkdirAll(d, 0o755)
	}
	return d
}

// webviewDataPath keeps the WebView2 cache next to the exe too, so nothing is
// left in the user profile.
func webviewDataPath() string {
	return filepath.Join(dataDir(), "Waymark data", "webview")
}
