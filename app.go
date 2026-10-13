package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/arthur044/screensharer/internal/adb"
	"github.com/arthur044/screensharer/internal/demo"
	"github.com/arthur044/screensharer/internal/media"
	"github.com/arthur044/screensharer/internal/platform"
	"github.com/arthur044/screensharer/internal/scrcpy"
	"github.com/arthur044/screensharer/internal/stream"
)

// Flags vêm da linha de comando (main.go).
type Flags struct {
	Debug      bool
	ADBPath    string
	ServerPath string
	Autostart  string // "demo" ou um serial
}

// Settings são as preferências salvas entre execuções.
type Settings struct {
	MaxSize     int  `json:"maxSize"`     // 0 = resolução do aparelho
	MaxFPS      int  `json:"maxFps"`      // 0 = sem limite
	BitRateMbps int  `json:"bitRateMbps"` //
	Control     bool `json:"control"`     // mouse e teclado controlam o celular
	StayAwake   bool `json:"stayAwake"`   // tela ligada enquanto conectado
}

func defaultSettings() Settings {
	return Settings{MaxSize: 1920, MaxFPS: 60, BitRateMbps: 8, Control: true, StayAwake: true}
}

// App concentra o estado e os métodos expostos à interface (bindings do Wails).
type App struct {
	ctx     context.Context
	flags   Flags
	log     *slog.Logger
	dataDir string
	stream  *stream.Server

	adb    *adb.Client
	adbErr error

	mu       sync.Mutex
	settings Settings
	src      media.Source
	session  *scrcpy.Session
	srvPath  string
	devices  []adb.Device
	// Estado da janela antes de o Hyprland ajustá-la ao vídeo (nil: não mexeu).
	winBefore *platform.WindowState
}

func NewApp(flags Flags, log *slog.Logger, dataDir string) *App {
	a := &App{flags: flags, log: log, dataDir: dataDir, settings: defaultSettings()}
	if b, err := os.ReadFile(a.settingsPath()); err == nil {
		json.Unmarshal(b, &a.settings)
	}
	return a
}

func (a *App) settingsPath() string { return filepath.Join(a.dataDir, "settings.json") }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	s, err := stream.New(a.log)
	if err != nil {
		a.log.Error("abrindo o servidor de vídeo local", "err", err)
	}
	a.stream = s
	path, err := platform.FindADB(a.flags.ADBPath)
	if err != nil {
		a.adbErr = errors.New("adb não encontrado: instale o Android platform-tools ou use --adb CAMINHO")
		a.log.Warn("adb não encontrado")
	} else {
		a.adb = &adb.Client{Path: path}
		a.log.Info("usando adb", "path", path)
		go a.watchDevices()
	}
}

func (a *App) shutdown(context.Context) {
	a.StopMirror()
	if a.stream != nil {
		a.stream.Close()
	}
}

// watchDevices consulta o adb a cada 1,5 s e avisa a interface quando a lista muda.
func (a *App) watchDevices() {
	for {
		devs, err := a.adb.Devices()
		if err != nil {
			// Mantém a última lista: uma falha passageira do adb não deve
			// fazer os aparelhos sumirem da tela.
			a.log.Debug("adb devices", "err", err)
		} else {
			a.mu.Lock()
			changed := !reflect.DeepEqual(devs, a.devices)
			a.devices = devs
			a.mu.Unlock()
			if changed {
				wruntime.EventsEmit(a.ctx, "devices", devs)
			}
		}
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(1500 * time.Millisecond):
		}
	}
}

// EnvInfo descreve o ambiente para a tela de diagnóstico.
type EnvInfo struct {
	OS            string `json:"os"`
	ADBPath       string `json:"adbPath"`
	ADBVersion    string `json:"adbVersion"`
	ADBError      string `json:"adbError"`
	ServerVersion string `json:"serverVersion"`
	DemoAvailable bool   `json:"demoAvailable"`
	Debug         bool   `json:"debug"`
	Autostart     string `json:"autostart"`
	DataDir       string `json:"dataDir"`
}

func (a *App) Environment() EnvInfo {
	info := EnvInfo{
		OS:            runtime.GOOS,
		ServerVersion: scrcpy.ServerVersion,
		DemoAvailable: demo.Available(),
		Debug:         a.flags.Debug,
		Autostart:     a.flags.Autostart,
		DataDir:       a.dataDir,
	}
	if a.adb == nil {
		if a.adbErr != nil {
			info.ADBError = a.adbErr.Error()
		}
		return info
	}
	info.ADBPath = a.adb.Path
	v, err := a.adb.Version()
	info.ADBVersion = v
	if err != nil {
		info.ADBError = err.Error()
	}
	return info
}

func (a *App) needADB() error {
	if a.adb == nil {
		return a.adbErr
	}
	return nil
}

// ListDevices consulta o adb e devolve a lista de aparelhos.
func (a *App) ListDevices() ([]adb.Device, error) {
	if err := a.needADB(); err != nil {
		return nil, err
	}
	return a.adb.Devices()
}

func (a *App) GetSettings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

func (a *App) SaveSettings(s Settings) error {
	a.mu.Lock()
	a.settings = s
	a.mu.Unlock()
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(a.settingsPath(), b, 0o644)
}

// MirrorInfo é o que a interface precisa para mostrar o vídeo.
type MirrorInfo struct {
	URL        string `json:"url"`
	DeviceName string `json:"deviceName"`
	Control    bool   `json:"control"`
}

// StartMirror inicia o espelhamento de um aparelho (USB ou Wi-Fi).
func (a *App) StartMirror(serial string) (MirrorInfo, error) {
	if err := a.needADB(); err != nil {
		return MirrorInfo{}, err
	}
	if a.stream == nil {
		return MirrorInfo{}, errors.New("servidor de vídeo local indisponível")
	}
	a.StopMirror()
	srv, err := a.serverPath()
	if err != nil {
		return MirrorInfo{}, err
	}
	set := a.GetSettings()
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	sess, err := scrcpy.Start(ctx, a.adb, serial, srv, scrcpy.Options{
		MaxSize:   set.MaxSize,
		MaxFPS:    set.MaxFPS,
		BitRate:   set.BitRateMbps * 1_000_000,
		Control:   set.Control,
		StayAwake: set.StayAwake,
		OnClipboard: func(text string) {
			wruntime.ClipboardSetText(a.ctx, text)
		},
	}, a.log)
	if err != nil {
		a.log.Error("iniciando espelhamento", "serial", serial, "err", err)
		return MirrorInfo{}, err
	}
	a.attach(sess, sess)
	wruntime.WindowSetTitle(a.ctx, sess.DeviceName+" — screensharer")
	a.log.Info("espelhando", "serial", serial, "device", sess.DeviceName)
	return MirrorInfo{URL: a.stream.URL(), DeviceName: sess.DeviceName, Control: set.Control}, nil
}

// StartDemo transmite a fonte de teste (ffmpeg) pelo mesmo caminho do celular.
func (a *App) StartDemo() (MirrorInfo, error) {
	if a.stream == nil {
		return MirrorInfo{}, errors.New("servidor de vídeo local indisponível")
	}
	if !demo.Available() {
		return MirrorInfo{}, errors.New("a fonte de teste precisa do ffmpeg no PATH")
	}
	a.StopMirror()
	set := a.GetSettings()
	w, h := 720, 1600 // proporção de celular em pé
	if set.MaxSize > 0 && set.MaxSize < h {
		w, h = w*set.MaxSize/h/16*16, set.MaxSize/16*16
	}
	fps := set.MaxFPS
	if fps == 0 {
		fps = 60
	}
	src, err := demo.Start(w, h, fps, set.BitRateMbps*1_000_000, a.log)
	if err != nil {
		return MirrorInfo{}, err
	}
	a.attach(src, nil)
	wruntime.WindowSetTitle(a.ctx, "Fonte de teste — screensharer")
	return MirrorInfo{URL: a.stream.URL(), DeviceName: "Fonte de teste", Control: set.Control}, nil
}

func (a *App) attach(src media.Source, sess *scrcpy.Session) {
	a.mu.Lock()
	a.src, a.session = src, sess
	a.mu.Unlock()
	a.keepAwake(true)
	a.stream.Attach(src, a.handleInput, func(err error) {
		a.mu.Lock()
		current := a.src == src
		if current {
			a.src, a.session = nil, nil
		}
		a.mu.Unlock()
		src.Close()
		if current {
			a.restoreWindow()
			a.keepAwake(false)
			reason := ""
			if err != nil {
				reason = err.Error()
				a.log.Warn("espelhamento terminou", "err", err)
			}
			wruntime.EventsEmit(a.ctx, "mirror:end", reason)
			wruntime.WindowSetTitle(a.ctx, "screensharer")
		}
	})
}

func (a *App) handleInput(in media.Input) {
	a.mu.Lock()
	src, sess := a.src, a.session
	a.mu.Unlock()
	if src == nil {
		return
	}
	var err error
	if in.T == "log" {
		a.log.Debug("interface", "texto", in.S)
		return
	}
	if in.T == "window" {
		a.fitWindow(src, in.W, in.H)
		return
	}
	if in.T == "paste" {
		if sess == nil {
			return
		}
		var text string
		text, err = wruntime.ClipboardGetText(a.ctx)
		if err == nil && text != "" {
			err = sess.SetClipboard(text, true)
		}
	} else {
		err = src.HandleInput(in)
	}
	if err != nil {
		// Só o tipo: o evento pode trazer texto digitado (senhas).
		a.log.Debug("entrada", "tipo", in.T, "err", err)
	}
}

// fitWindow atende o pedido da interface de dar à janela o tamanho do vídeo.
// Só o Hyprland precisa: nos outros sistemas a própria interface redimensiona.
func (a *App) fitWindow(src media.Source, w, h int) {
	if !platform.OnHyprland() || w <= 0 || h <= 0 {
		return
	}
	before, ok, err := platform.HyprlandFit(w, h)
	if err != nil {
		a.log.Warn("ajuste da janela", "err", err)
	}
	if !ok {
		return
	}
	a.mu.Lock()
	current := a.src == src
	if current && a.winBefore == nil {
		a.winBefore = &before
	}
	a.mu.Unlock()
	// O espelhamento acabou enquanto o hyprctl rodava: restoreWindow já passou
	// sem ter o que desfazer, então desfaz aqui.
	if !current {
		if err := platform.HyprlandRestore(before); err != nil {
			a.log.Warn("restauração da janela", "err", err)
		}
	}
}

// restoreWindow desfaz o fitWindow quando o espelhamento termina.
func (a *App) restoreWindow() {
	a.mu.Lock()
	before := a.winBefore
	a.winBefore = nil
	a.mu.Unlock()
	if before == nil {
		return
	}
	if err := platform.HyprlandRestore(*before); err != nil {
		a.log.Warn("restauração da janela", "err", err)
	}
}

// StopMirror encerra o espelhamento atual, se houver.
func (a *App) StopMirror() {
	a.mu.Lock()
	src := a.src
	a.src, a.session = nil, nil
	a.mu.Unlock()
	if src != nil {
		if a.stream != nil {
			a.stream.Detach()
		}
		src.Close()
		a.restoreWindow()
		a.keepAwake(false)
		wruntime.WindowSetTitle(a.ctx, "screensharer")
	}
}

// keepAwake segura (on) ou solta (off) o protetor de tela e o bloqueio do PC
// enquanto há espelhamento.
func (a *App) keepAwake(on bool) {
	if err := platform.KeepAwake(on); err != nil {
		a.log.Warn("mantendo a tela do PC ligada", "err", err)
	}
}

func (a *App) serverPath() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.srvPath != "" {
		return a.srvPath, nil
	}
	p, err := scrcpy.FindServer(a.flags.ServerPath, a.dataDir)
	if err != nil {
		return "", err
	}
	a.srvPath = p
	return p, nil
}

// --- Wi-Fi ---

// Pair pareia com o código mostrado no celular (Depuração sem fio → Parear com código).
func (a *App) Pair(hostPort, code string) error {
	if err := a.needADB(); err != nil {
		return err
	}
	return a.adb.Pair(hostPort, code)
}

// Connect conecta num aparelho já pareado.
func (a *App) Connect(hostPort string) error {
	if err := a.needADB(); err != nil {
		return err
	}
	return a.adb.Connect(hostPort)
}

// Disconnect desconecta um aparelho Wi-Fi.
func (a *App) Disconnect(serial string) error {
	if err := a.needADB(); err != nil {
		return err
	}
	return a.adb.Disconnect(serial)
}

// DiscoverWiFi lista aparelhos com depuração sem fio anunciados na rede.
func (a *App) DiscoverWiFi() ([]adb.MDNSService, error) {
	if err := a.needADB(); err != nil {
		return nil, err
	}
	return a.adb.MDNSServices()
}

// EnableWiFiResult é a resposta do botão "Ativar Wi-Fi".
type EnableWiFiResult struct {
	IP           string `json:"ip"`
	Address      string `json:"address"`
	Connected    bool   `json:"connected"`
	NeedsPairing bool   `json:"needsPairing"`
	Message      string `json:"message"`
}

// EnableWiFi liga a depuração sem fio de um aparelho conectado pelo USB.
func (a *App) EnableWiFi(serial string) (EnableWiFiResult, error) {
	if err := a.needADB(); err != nil {
		return EnableWiFiResult{}, err
	}
	r, err := a.adb.EnableWiFi(serial)
	res := EnableWiFiResult{IP: r.IP, Address: r.Address}
	switch {
	case err == nil:
		res.Connected = true
		res.Message = fmt.Sprintf("Conectado pelo Wi-Fi em %s. Já pode tirar o cabo.", r.Address)
		a.log.Info("wi-fi ativado sem pareamento", "serial", serial, "address", r.Address)
		return res, nil
	case errors.Is(err, adb.ErrNeedsPairing):
		res.NeedsPairing = true
		res.Message = "A depuração sem fio foi ligada, mas este PC ainda precisa ser pareado. No celular, abra Depuração sem fio → Parear dispositivo com código de pareamento."
		a.log.Info("wi-fi exige pareamento", "serial", serial, "err", err)
		return res, nil
	default:
		return res, err
	}
}
