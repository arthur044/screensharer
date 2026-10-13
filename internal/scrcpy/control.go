package scrcpy

import (
	"encoding/binary"
	"math"
	"unicode/utf8"
)

// Tipos de mensagem de controle (PC → celular), na ordem de
// app/src/control_msg.h do scrcpy v5.0.1.
const (
	msgInjectKeycode byte = iota
	msgInjectText
	msgInjectTouch
	msgInjectScroll
	msgBackOrScreenOn
	msgExpandNotificationPanel
	msgExpandSettingsPanel
	msgCollapsePanels
	msgGetClipboard
	msgSetClipboard
	msgSetDisplayPower
	msgRotateDevice
	msgUhidCreate
	msgUhidInput
	msgUhidDestroy
	msgOpenHardKeyboardSettings
	msgStartApp
	msgResetVideo
)

// Ações e botões do Android (android/input.h).
const (
	ActionDown = 0
	ActionUp   = 1
	ActionMove = 2

	ButtonPrimary   = 1 << 0
	ButtonSecondary = 1 << 1
	ButtonTertiary  = 1 << 2
)

// PointerIDMouse identifica o ponteiro do mouse (SC_POINTER_ID_MOUSE = -1).
const PointerIDMouse uint64 = math.MaxUint64

const (
	injectTextMaxLen    = 300
	clipboardTextMaxLen = 1<<18 - 14
)

// Position é um ponto no frame de vídeo atual. O servidor ignora eventos cujo
// ScreenW/ScreenH não batem com o tamanho do vídeo naquele momento.
type Position struct {
	X, Y             int32
	ScreenW, ScreenH uint16
}

func putPosition(b []byte, p Position) {
	binary.BigEndian.PutUint32(b[0:], uint32(p.X))
	binary.BigEndian.PutUint32(b[4:], uint32(p.Y))
	binary.BigEndian.PutUint16(b[8:], p.ScreenW)
	binary.BigEndian.PutUint16(b[10:], p.ScreenH)
}

// truncUTF8 corta s em no máximo max bytes sem quebrar um caractere.
func truncUTF8(s string, max int) []byte {
	b := []byte(s)
	if len(b) <= max {
		return b
	}
	b = b[:max]
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return b
}

func putString(prefix []byte, s string, max int) []byte {
	p := truncUTF8(s, max)
	out := make([]byte, len(prefix)+4+len(p))
	copy(out, prefix)
	binary.BigEndian.PutUint32(out[len(prefix):], uint32(len(p)))
	copy(out[len(prefix)+4:], p)
	return out
}

// InjectKeycode monta INJECT_KEYCODE (14 bytes).
func InjectKeycode(action byte, keycode, repeat, metastate uint32) []byte {
	b := make([]byte, 14)
	b[0] = msgInjectKeycode
	b[1] = action
	binary.BigEndian.PutUint32(b[2:], keycode)
	binary.BigEndian.PutUint32(b[6:], repeat)
	binary.BigEndian.PutUint32(b[10:], metastate)
	return b
}

// InjectText monta INJECT_TEXT (texto UTF-8, até 300 bytes).
func InjectText(text string) []byte {
	return putString([]byte{msgInjectText}, text, injectTextMaxLen)
}

// InjectTouch monta INJECT_TOUCH_EVENT (32 bytes). pressure vai de 0 a 1.
func InjectTouch(action byte, pointerID uint64, pos Position, pressure float32, actionButton, buttons uint32) []byte {
	b := make([]byte, 32)
	b[0] = msgInjectTouch
	b[1] = action
	binary.BigEndian.PutUint64(b[2:], pointerID)
	putPosition(b[10:], pos)
	binary.BigEndian.PutUint16(b[22:], floatToU16FP(pressure))
	binary.BigEndian.PutUint32(b[24:], actionButton)
	binary.BigEndian.PutUint32(b[28:], buttons)
	return b
}

// InjectScroll monta INJECT_SCROLL_EVENT (21 bytes). hscroll e vscroll ficam
// em [-16, 16], como no cliente oficial.
func InjectScroll(pos Position, hscroll, vscroll float32, buttons uint32) []byte {
	b := make([]byte, 21)
	b[0] = msgInjectScroll
	putPosition(b[1:], pos)
	binary.BigEndian.PutUint16(b[13:], uint16(floatToI16FP(clamp(hscroll/16, -1, 1))))
	binary.BigEndian.PutUint16(b[15:], uint16(floatToI16FP(clamp(vscroll/16, -1, 1))))
	binary.BigEndian.PutUint32(b[17:], buttons)
	return b
}

// BackOrScreenOn envia BACK, ou liga a tela se ela estiver apagada.
func BackOrScreenOn(action byte) []byte { return []byte{msgBackOrScreenOn, action} }

// ExpandNotificationPanel abre a cortina de notificações.
func ExpandNotificationPanel() []byte { return []byte{msgExpandNotificationPanel} }

// CollapsePanels fecha a cortina de notificações e as configurações rápidas.
func CollapsePanels() []byte { return []byte{msgCollapsePanels} }

// SetClipboard copia text para o clipboard do celular; com paste, também cola.
func SetClipboard(sequence uint64, text string, paste bool) []byte {
	prefix := make([]byte, 10)
	prefix[0] = msgSetClipboard
	binary.BigEndian.PutUint64(prefix[1:], sequence)
	if paste {
		prefix[9] = 1
	}
	return putString(prefix, text, clipboardTextMaxLen)
}

// SetDisplayPower liga ou desliga a tela do celular sem parar o espelhamento.
func SetDisplayPower(on bool) []byte {
	if on {
		return []byte{msgSetDisplayPower, 1}
	}
	return []byte{msgSetDisplayPower, 0}
}

// RotateDevice gira a tela do celular.
func RotateDevice() []byte { return []byte{msgRotateDevice} }

// ResetVideo pede ao servidor que reinicie o encoder, o que gera um keyframe.
func ResetVideo() []byte { return []byte{msgResetVideo} }

func clamp(f, lo, hi float32) float32 {
	return float32(math.Max(float64(lo), math.Min(float64(hi), float64(f))))
}

// floatToU16FP replica sc_float_to_u16fp (app/src/util/binary.h).
func floatToU16FP(f float32) uint16 {
	f = clamp(f, 0, 1)
	u := uint32(f * 65536)
	if u >= 0xffff {
		u = 0xffff
	}
	return uint16(u)
}

// floatToI16FP replica sc_float_to_i16fp.
func floatToI16FP(f float32) int16 {
	i := int32(f * 32768)
	if i >= 0x7fff {
		i = 0x7fff
	}
	return int16(i)
}
