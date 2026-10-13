package scrcpy

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/arthur044/screensharer/internal/adb"
	"github.com/arthur044/screensharer/internal/media"
)

// TestSessionWithFakeDevice usa um adb falso (script) e um "celular" falso
// (listener TCP) que fala o protocolo do scrcpy-server v5.0.1.
func TestSessionWithFakeDevice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o adb falso é um script sh")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	dir := t.TempDir()
	fakeADB := filepath.Join(dir, "adb")
	script := "#!/bin/sh\n" +
		"case \"$3\" in\n" +
		"  forward) [ \"$4\" = --remove ] || echo " + strconv.Itoa(port) + " ;;\n" +
		"  shell) exec sleep 30 ;;\n" +
		"esac\n"
	if err := os.WriteFile(fakeADB, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := filepath.Join(dir, "server.jar")
	os.WriteFile(srv, []byte("x"), 0o644)

	ctrlGot := make(chan []byte, 1)
	go func() {
		// Primeiro socket: vídeo. O primeiro dial fecha sem o byte de "pronto",
		// simulando o servidor ainda subindo.
		c, err := ln.Accept()
		if err != nil {
			return
		}
		c.Close()
		video, err := ln.Accept()
		if err != nil {
			return
		}
		defer video.Close()
		video.Write([]byte{0})
		// A ordem das conexões é vídeo, áudio, controle.
		audio, err := ln.Accept()
		if err != nil {
			return
		}
		defer audio.Close()
		ctrl, err := ln.Accept()
		if err != nil {
			return
		}
		defer ctrl.Close()

		var f fakeVideo
		name := make([]byte, 64)
		copy(name, "Pixel Teste")
		f.Write(name)
		f.u32(codecH264)
		f.session(720, 1600)
		f.packet(flagConfig, testSPSPPS)
		f.packet(flagKeyFrame|5, testIDR)
		video.Write(f.Bytes())

		var a fakeVideo
		a.u32(codecRaw)
		a.packet(1000, []byte{1, 0, 2, 0})
		audio.Write(a.Bytes())

		ctrl.Write([]byte{deviceMsgClipboard, 0, 0, 0, 2, 'o', 'i'})
		buf := make([]byte, 14)
		io.ReadFull(ctrl, buf)
		ctrlGot <- buf
		time.Sleep(200 * time.Millisecond)
	}()

	clip := make(chan string, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := Start(ctx, &adb.Client{Path: fakeADB}, "SERIAL", srv, Options{Control: true, Audio: true, OnClipboard: func(t string) { clip <- t }}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if s.DeviceName != "Pixel Teste" {
		t.Fatalf("nome = %q", s.DeviceName)
	}
	// O áudio vem de outro socket: chega em qualquer ponto entre os eventos de vídeo.
	var evs []media.Event
	var audio *media.Audio
	for e := range s.Events() {
		if e.Audio != nil {
			audio = e.Audio
		} else {
			evs = append(evs, e)
		}
		if len(evs) == 3 && audio != nil {
			break
		}
	}
	if evs[0].Session == nil || evs[1].Config == nil || evs[2].Packet == nil || !evs[2].Packet.Key {
		t.Fatalf("eventos inesperados: %+v", evs)
	}
	if audio.PTS != 1000 || !bytes.Equal(audio.Data, []byte{1, 0, 2, 0}) {
		t.Fatalf("áudio inesperado: %+v", audio)
	}
	select {
	case got := <-clip:
		if got != "oi" {
			t.Fatalf("clipboard = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("clipboard do celular não chegou")
	}
	if err := s.HandleInput(media.Input{T: "key", Action: ActionDown, Code: 3}); err != nil {
		t.Fatal(err)
	}
	if got := <-ctrlGot; !bytes.Equal(got, InjectKeycode(ActionDown, 3, 0, 0)) {
		t.Fatalf("controle recebido: % x", got)
	}
}
