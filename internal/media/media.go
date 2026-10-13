// Package media define o que todas as fontes de vídeo entregam ao resto do app
// (o VideoSource do plano): eventos de sessão, de configuração e pacotes H.264.
package media

import "fmt"

// Event é uma das três coisas que uma fonte produz. Só um dos campos vem preenchido.
type Event struct {
	Session *Session // nova sessão de captura (início ou rotação)
	Config  *Config  // SPS/PPS novos: o decoder deve ser (re)configurado
	Packet  *Packet  // uma access unit H.264 em Annex B
}

// Session traz o tamanho do vídeo da sessão de captura atual.
type Session struct {
	Width, Height int
}

// Config traz o codec string para o VideoDecoder (ex.: "avc1.42C01F").
type Config struct {
	Codec string
}

// Packet é uma access unit. Em keyframes, o SPS/PPS já vem na frente dos dados.
type Packet struct {
	Data []byte
	PTS  int64 // microssegundos
	Key  bool
}

// Input é um evento de entrada vindo da interface (mouse, teclado, botões).
// O formato é o JSON enviado pelo frontend (frontend/src/input.ts).
type Input struct {
	T      string  `json:"t"`              // touch, scroll, key, text, back, cmd, paste, copy, log, window
	Action int     `json:"a,omitempty"`    // ação Android (0 down, 1 up, 2 move)
	X      int     `json:"x,omitempty"`    // posição no frame de vídeo
	Y      int     `json:"y,omitempty"`    //
	W      int     `json:"w,omitempty"`    // tamanho do frame de vídeo (em "window", o da janela)
	H      int     `json:"h,omitempty"`    //
	Btn    uint32  `json:"b,omitempty"`    // botões pressionados
	ABtn   uint32  `json:"ab,omitempty"`   // botão que mudou de estado
	HS     float32 `json:"hs,omitempty"`   // rolagem horizontal
	VS     float32 `json:"vs,omitempty"`   // rolagem vertical
	Code   uint32  `json:"code,omitempty"` // keycode Android
	Meta   uint32  `json:"meta,omitempty"` // metastate Android
	Repeat uint32  `json:"repeat,omitempty"`
	S      string  `json:"s,omitempty"` // texto, ou nome do comando em "cmd"
}

// Source é uma fonte de vídeo: scrcpy (USB ou Wi-Fi) ou a fonte de teste.
type Source interface {
	// Events é fechado quando a fonte termina; Err diz o motivo.
	Events() <-chan Event
	Err() error
	// RequestKeyframe pede um keyframe novo (depois de descarte ou reconexão).
	RequestKeyframe()
	// HandleInput aplica um evento de entrada. Fontes sem controle ignoram.
	HandleInput(in Input) error
	Close() error
}

// H264Codec monta o codec string "avc1.PPCCLL" a partir de uma access unit ou
// de um pacote de configuração em Annex B que contenha o SPS.
func H264Codec(annexB []byte) (string, bool) {
	for _, nal := range SplitNALs(annexB) {
		if len(nal) >= 4 && nal[0]&0x1f == 7 {
			return fmt.Sprintf("avc1.%02X%02X%02X", nal[1], nal[2], nal[3]), true
		}
	}
	return "", false
}

// HasIDR diz se a access unit contém um slice IDR (NAL tipo 5).
func HasIDR(annexB []byte) bool {
	for _, nal := range SplitNALs(annexB) {
		if len(nal) > 0 && nal[0]&0x1f == 5 {
			return true
		}
	}
	return false
}

// SplitNALs separa um buffer Annex B em NAL units (sem os start codes).
func SplitNALs(b []byte) [][]byte {
	var nals [][]byte
	start := -1
	for i := 0; i+2 < len(b); {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			if start >= 0 {
				end := i
				if end > start && b[end-1] == 0 {
					end-- // start code de 4 bytes
				}
				nals = append(nals, b[start:end])
			}
			i += 3
			start = i
			continue
		}
		i++
	}
	if start >= 0 && start < len(b) {
		nals = append(nals, b[start:])
	}
	return nals
}
