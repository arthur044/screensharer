package scrcpy

import (
	"bytes"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/arthur044/screensharer/internal/media"
)

// O scrcpy-server v5.0.1 morre com NullPointerException se receber RESET_VIDEO
// antes de a captura existir (visto num Galaxy A71, Android 13). O pedido de
// keyframe só pode sair depois da primeira sessão de vídeo.
func TestRequestKeyframeWaitsForVideo(t *testing.T) {
	videoPhone, videoPC := net.Pipe()
	ctrlPhone, ctrlPC := net.Pipe()
	defer videoPhone.Close()
	defer ctrlPhone.Close()

	s := &Session{
		video:  videoPC,
		ctrl:   ctrlPC,
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		srvOut: &lockedBuffer{},
		events: make(chan media.Event, 4),
		done:   make(chan struct{}),
	}
	defer s.Close()
	go s.readLoop()

	// Tudo o que o PC escreve no canal de controle.
	got := make(chan []byte, 4)
	go func() {
		buf := make([]byte, 64)
		for {
			n, err := ctrlPhone.Read(buf)
			if err != nil {
				return
			}
			got <- append([]byte(nil), buf[:n]...)
		}
	}()

	s.RequestKeyframe()
	select {
	case msg := <-got:
		t.Fatalf("pedido de keyframe antes do vídeo começar: % x", msg)
	case <-time.After(200 * time.Millisecond):
	}

	var hdr [16]byte
	binary.BigEndian.PutUint32(hdr[0:], codecH264)
	binary.BigEndian.PutUint64(hdr[4:], flagSession|720)
	binary.BigEndian.PutUint32(hdr[12:], 1600)
	if _, err := videoPhone.Write(hdr[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-s.Events():
		if e.Session == nil || e.Session.Width != 720 || e.Session.Height != 1600 {
			t.Fatalf("evento inesperado: %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a sessão de vídeo não chegou")
	}

	s.RequestKeyframe()
	select {
	case msg := <-got:
		if !bytes.Equal(msg, ResetVideo()) {
			t.Fatalf("controle recebido: % x", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("o pedido de keyframe não saiu depois do vídeo começar")
	}
}
