package scrcpy

import (
	"bytes"
	"strings"
	"testing"
)

// Os bytes esperados vêm de app/tests/test_control_msg_serialize.c do scrcpy v5.0.1.

func TestInjectKeycode(t *testing.T) {
	got := InjectKeycode(ActionUp, 66 /* ENTER */, 5, 0x41)
	want := []byte{0, 0x01, 0, 0, 0, 0x42, 0, 0, 0, 0x05, 0, 0, 0, 0x41}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestInjectText(t *testing.T) {
	got := InjectText("hello, world!")
	want := append([]byte{1, 0, 0, 0, 0x0d}, "hello, world!"...)
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestInjectTextTruncates(t *testing.T) {
	got := InjectText(strings.Repeat("a", 400))
	if len(got) != 5+300 || got[3] != 0x01 || got[4] != 0x2c {
		t.Fatalf("len=%d header=% x", len(got), got[:5])
	}
	// Não pode cortar um caractere multibyte no meio.
	got = InjectText(strings.Repeat("a", 299) + "é")
	if len(got) != 5+299 {
		t.Fatalf("UTF-8 cortado errado: len=%d", len(got))
	}
}

func TestInjectTouch(t *testing.T) {
	got := InjectTouch(ActionDown, 0x1234567887654321, Position{100, 200, 1080, 1920}, 1, ButtonPrimary, ButtonPrimary)
	want := []byte{
		2, 0x00,
		0x12, 0x34, 0x56, 0x78, 0x87, 0x65, 0x43, 0x21,
		0, 0, 0, 0x64, 0, 0, 0, 0xc8,
		0x04, 0x38, 0x07, 0x80,
		0xff, 0xff,
		0, 0, 0, 1,
		0, 0, 0, 1,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestInjectScroll(t *testing.T) {
	got := InjectScroll(Position{260, 1026, 1080, 1920}, 16, -16, 1)
	want := []byte{
		3,
		0, 0, 0x01, 0x04, 0, 0, 0x04, 0x02,
		0x04, 0x38, 0x07, 0x80,
		0x7f, 0xff,
		0x80, 0x00,
		0, 0, 0, 1,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestSetClipboard(t *testing.T) {
	got := SetClipboard(0x0102030405060708, "hello, world!", true)
	want := append([]byte{9, 1, 2, 3, 4, 5, 6, 7, 8, 1, 0, 0, 0, 0x0d}, "hello, world!"...)
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestSimpleMessages(t *testing.T) {
	cases := map[string]struct{ got, want []byte }{
		"back":     {BackOrScreenOn(ActionDown), []byte{4, 0}},
		"notif":    {ExpandNotificationPanel(), []byte{5}},
		"collapse": {CollapsePanels(), []byte{7}},
		"power":    {SetDisplayPower(false), []byte{10, 0}},
		"rotate":   {RotateDevice(), []byte{11}},
		"reset":    {ResetVideo(), []byte{17}},
	}
	for name, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s: got % x, want % x", name, c.got, c.want)
		}
	}
}
