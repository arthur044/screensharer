package demo

import (
	"bytes"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestSplitAccessUnits(t *testing.T) {
	in := []byte{0, 0, 0, 1, 9, 0xf0, 0, 0, 0, 1, 0x65, 1, 0, 0, 0, 1, 9, 0xf0, 0, 0, 0, 1, 0x41, 2}
	var aus [][]byte
	splitAccessUnits(bytes.NewReader(in), func(b []byte) { aus = append(aus, b) })
	if len(aus) != 2 || len(aus[0]) != 12 || aus[1][10] != 0x41 {
		t.Fatalf("aus = % x", aus)
	}
}

func TestSourceWithFFmpeg(t *testing.T) {
	if !Available() {
		t.Skip("ffmpeg não instalado")
	}
	s, err := Start(320, 240, 30, 1_000_000, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var session, config, key bool
	timeout := time.After(10 * time.Second)
	for !(session && config && key) {
		select {
		case e := <-s.Events():
			session = session || e.Session != nil
			config = config || e.Config != nil
			key = key || (e.Packet != nil && e.Packet.Key)
		case <-timeout:
			t.Fatalf("session=%v config=%v key=%v", session, config, key)
		}
	}
}
