package main

import (
	"embed"
	"flag"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/logger"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"

	"github.com/arthur044/screensharer/internal/platform"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	var f Flags
	flag.BoolVar(&f.Debug, "debug", false, "logs detalhados e estatísticas na tela")
	flag.StringVar(&f.ADBPath, "adb", "", "caminho do executável adb")
	flag.StringVar(&f.ServerPath, "server", "", "caminho de um scrcpy-server já baixado")
	flag.StringVar(&f.Autostart, "autostart", "", `começa a espelhar ao abrir: "demo" ou o serial do aparelho`)
	flag.Parse()

	dataDir, err := platform.DataDir()
	if err != nil {
		dataDir = os.TempDir()
	}
	log := newLogger(f.Debug, dataDir)
	app := NewApp(f, log, dataDir)

	err = wails.Run(&options.App{
		Title:            "screensharer",
		Width:            1100,
		Height:           800,
		MinWidth:         420,
		MinHeight:        420,
		BackgroundColour: &options.RGBA{R: 16, G: 18, B: 22, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
		LogLevel:         logger.WARNING,
		Linux:            &linux.Options{ProgramName: "screensharer"},
	})
	if err != nil {
		log.Error("wails", "err", err)
		os.Exit(1)
	}
}

// newLogger escreve no stderr e em screensharer.log na pasta de dados, para
// diagnosticar problemas em aparelhos reais.
func newLogger(debug bool, dataDir string) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	var w io.Writer = os.Stderr
	if file, err := os.OpenFile(filepath.Join(dataDir, "screensharer.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
		w = io.MultiWriter(os.Stderr, file)
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}
