package platform

import "testing"

func TestFitArea(t *testing.T) {
	// Monitor 1920×1080 em escala 1,5 com uma barra de 26 px em cima: sobram 1280×694.
	m := hyprMonitor{Width: 1920, Height: 1080, Scale: 1.5, Reserved: [4]int{0, 26, 0, 0}}
	tests := []struct {
		name               string
		w, h, wantW, wantH int
	}{
		{"cabe: não muda", 324, 600, 324, 600},
		{"alto demais: reduz mantendo a proporção", 324, 720, 312, 694},
		{"largo demais", 2560, 694, 1280, 347},
	}
	for _, tt := range tests {
		if w, h := fitArea(tt.w, tt.h, m); w != tt.wantW || h != tt.wantH {
			t.Errorf("%s: fitArea(%d, %d) = %d×%d, quero %d×%d", tt.name, tt.w, tt.h, w, h, tt.wantW, tt.wantH)
		}
	}
	// Monitor em pé: largura e altura trocam.
	m = hyprMonitor{Width: 1920, Height: 1080, Scale: 1, Transform: 1}
	if w, h := fitArea(1080, 2400, m); w != 864 || h != 1920 {
		t.Errorf("monitor girado: fitArea = %d×%d, quero 864×1920", w, h)
	}
}
