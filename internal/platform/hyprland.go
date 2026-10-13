package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Limite de cada chamada ao hyprctl: ela roda na goroutine que recebe o toque
// e o teclado, que não pode ficar presa.
const hyprTimeout = 2 * time.Second

// O Hyprland diz a toda janela que ela está em mosaico, e com isso o GTK
// ignora o pedido de tamanho do próprio app (gtk_window_resize). Quem muda o
// tamanho é o Hyprland, pelo hyprctl. A sintaxe é a de Lua (Hyprland 0.5x).

// WindowState é como a janela estava antes de HyprlandFit mexer nela.
type WindowState struct {
	Floating bool
	W, H     int
}

// OnHyprland diz se o app está rodando dentro do Hyprland.
func OnHyprland() bool {
	return os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != ""
}

type hyprClient struct {
	PID        int    `json:"pid"`
	Floating   bool   `json:"floating"`
	Fullscreen int    `json:"fullscreen"`
	Monitor    int    `json:"monitor"`
	Size       [2]int `json:"size"`
}

type hyprMonitor struct {
	ID        int     `json:"id"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	Scale     float64 `json:"scale"`
	Transform int     `json:"transform"`
	Reserved  [4]int  `json:"reserved"` // esquerda, cima, direita, baixo
}

// HyprlandFit deixa a janela deste processo flutuante, com o tamanho pedido e
// centralizada. Se não couber na área livre do monitor, reduz mantendo a
// proporção. ok é falso quando a janela está em tela cheia e nada foi feito.
func HyprlandFit(w, h int) (before WindowState, ok bool, err error) {
	var clients []hyprClient
	if err := hyprJSON(&clients, "clients"); err != nil {
		return before, false, err
	}
	var win *hyprClient
	for i := range clients {
		if clients[i].PID == os.Getpid() {
			win = &clients[i]
			break
		}
	}
	if win == nil {
		return before, false, fmt.Errorf("hyprland: janela do app não encontrada")
	}
	if win.Fullscreen != 0 {
		return before, false, nil
	}
	before = WindowState{Floating: win.Floating, W: win.Size[0], H: win.Size[1]}

	var monitors []hyprMonitor
	if err := hyprJSON(&monitors, "monitors"); err != nil {
		return before, false, err
	}
	for _, m := range monitors {
		if m.ID == win.Monitor && m.Scale > 0 {
			w, h = fitArea(w, h, m)
			break
		}
	}
	if !win.Floating {
		if err := hyprDispatch("float", "action = 'enable'"); err != nil {
			return before, false, err
		}
	}
	return before, true, hyprResize(w, h)
}

// HyprlandRestore devolve a janela ao estado de antes de HyprlandFit.
func HyprlandRestore(before WindowState) error {
	if !before.Floating {
		return hyprDispatch("float", "action = 'disable'")
	}
	return hyprResize(before.W, before.H)
}

// fitArea reduz w×h, mantendo a proporção, para caber na área livre do monitor.
func fitArea(w, h int, m hyprMonitor) (int, int) {
	mw, mh := float64(m.Width)/m.Scale, float64(m.Height)/m.Scale
	if m.Transform%2 == 1 { // monitor girado 90° ou 270°
		mw, mh = mh, mw
	}
	mw -= float64(m.Reserved[0] + m.Reserved[2])
	mh -= float64(m.Reserved[1] + m.Reserved[3])
	scale := math.Min(1, math.Min(mw/float64(w), mh/float64(h)))
	if scale <= 0 {
		return w, h
	}
	return int(float64(w) * scale), int(float64(h) * scale)
}

func hyprResize(w, h int) error {
	if err := hyprDispatch("resize", fmt.Sprintf("x = %d, y = %d", w, h)); err != nil {
		return err
	}
	return hyprDispatch("center", "")
}

// hyprIdleInhibit marca a janela do app como exibindo vídeo, para o Hyprland
// segurar o protetor de tela e o bloqueio. O valor é numérico (1 = sempre,
// 0 = nunca): o set_prop aceita os nomes sem erro, mas não os aplica.
func hyprIdleInhibit(on bool) error {
	value := 0
	if on {
		value = 1
	}
	return hyprDispatch("set_prop", fmt.Sprintf("prop = 'idle_inhibit', value = '%d'", value))
}

// hyprDispatch chama hl.dsp.window.<name> para a janela deste processo.
func hyprDispatch(name, args string) error {
	if args != "" {
		args += ", "
	}
	code := fmt.Sprintf("hl.dsp.window.%s({ %swindow = 'pid:%d' })", name, args, os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), hyprTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "hyprctl", "dispatch", code).CombinedOutput()
	if err != nil {
		return fmt.Errorf("hyprctl %s: %w", name, err)
	}
	// O hyprctl sai com 0 mesmo quando o Hyprland recusa o comando.
	if text := strings.TrimSpace(string(out)); text != "ok" {
		return fmt.Errorf("hyprctl %s: %s", name, text)
	}
	return nil
}

func hyprJSON(v any, what string) error {
	ctx, cancel := context.WithTimeout(context.Background(), hyprTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "hyprctl", "-j", what).Output()
	if err != nil {
		return fmt.Errorf("hyprctl %s: %w", what, err)
	}
	return json.Unmarshal(out, v)
}
