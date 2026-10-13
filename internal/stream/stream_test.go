package stream

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/arthur044/screensharer/internal/media"
)

type fakeSource struct {
	events  chan media.Event
	keyReqs atomic.Int32
	inputs  chan media.Input
}

func newFake() *fakeSource {
	return &fakeSource{events: make(chan media.Event, 16), inputs: make(chan media.Input, 4)}
}
func (f *fakeSource) Events() <-chan media.Event { return f.events }
func (f *fakeSource) Err() error                 { return nil }
func (f *fakeSource) RequestKeyframe()           { f.keyReqs.Add(1) }
func (f *fakeSource) HandleInput(in media.Input) error {
	f.inputs <- in
	return nil
}
func (f *fakeSource) Close() error { close(f.events); return nil }

func dial(t *testing.T, s *Server, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	h := http.Header{}
	if origin != "" {
		h.Set("Origin", origin)
	}
	return websocket.DefaultDialer.Dial(s.URL(), h)
}

func TestStream(t *testing.T) {
	s, err := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	src := newFake()
	ended := make(chan struct{})
	s.Attach(src, func(in media.Input) { src.HandleInput(in) }, func(error) { close(ended) })

	src.events <- media.Event{Session: &media.Session{Width: 720, Height: 1600}}
	src.events <- media.Event{Config: &media.Config{Codec: "avc1.42C01F"}}
	time.Sleep(50 * time.Millisecond) // o pump guarda o estado antes do cliente chegar

	c, _, err := dial(t, s, "wails://wails")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))

	readJSON := func() map[string]any {
		t.Helper()
		for {
			typ, b, err := c.ReadMessage()
			if err != nil {
				t.Fatal(err)
			}
			if typ != websocket.TextMessage {
				t.Fatalf("esperava JSON, veio binário")
			}
			var m map[string]any
			json.Unmarshal(b, &m)
			if m["type"] != "stats" {
				return m
			}
		}
	}
	if m := readJSON(); m["type"] != "session" || m["width"] != 720.0 {
		t.Fatalf("session: %v", m)
	}
	if m := readJSON(); m["type"] != "config" || m["codec"] != "avc1.42C01F" {
		t.Fatalf("config: %v", m)
	}
	if src.keyReqs.Load() == 0 {
		t.Fatal("cliente novo deveria pedir keyframe")
	}

	// Frame P antes do keyframe é descartado; o keyframe passa.
	src.events <- media.Event{Packet: &media.Packet{Data: []byte{0xAA}, PTS: 1}}
	src.events <- media.Event{Packet: &media.Packet{Data: []byte{0xBB}, PTS: 2, Key: true}}
	typ, b, err := c.ReadMessage()
	if err != nil || typ != websocket.BinaryMessage {
		t.Fatalf("typ=%d err=%v", typ, err)
	}
	if b[0] != 1 || b[1] != 1 || b[9] != 2 || b[10] != 0xBB {
		t.Fatalf("frame: % x", b)
	}

	c.WriteMessage(websocket.TextMessage, []byte(`{"t":"key","a":0,"code":3}`))
	select {
	case in := <-src.inputs:
		if in.T != "key" || in.Code != 3 {
			t.Fatalf("input: %+v", in)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("input não chegou")
	}

	src.Close()
	if m := readJSON(); m["type"] != "end" {
		t.Fatalf("end: %v", m)
	}
	<-ended
}

func TestStreamRejects(t *testing.T) {
	s, err := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, resp, err := dial(t, s, "https://evil.example"); err == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("origem estranha deveria ser recusada: %v", err)
	}
	bad := strings.Replace(s.URL(), "token=", "token=x", 1)
	if _, resp, err := websocket.DefaultDialer.Dial(bad, nil); err == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("token errado deveria ser recusado: %v", err)
	}
	for _, o := range []string{"wails://wails", "http://wails.localhost", "http://localhost:34115"} {
		if !allowedOrigin(o) {
			t.Errorf("%s deveria ser aceita", o)
		}
	}
}
