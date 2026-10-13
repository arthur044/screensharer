package scrcpy

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/arthur044/screensharer/internal/media"
)

// fakeVideo monta o que o servidor escreve no socket de vídeo.
type fakeVideo struct{ bytes.Buffer }

func (f *fakeVideo) u32(v uint32) { binary.Write(&f.Buffer, binary.BigEndian, v) }
func (f *fakeVideo) u64(v uint64) { binary.Write(&f.Buffer, binary.BigEndian, v) }

func (f *fakeVideo) session(w, h uint32) {
	f.u32(0x80000000)
	f.u32(w)
	f.u32(h)
}

func (f *fakeVideo) packet(ptsFlags uint64, data []byte) {
	f.u64(ptsFlags)
	f.u32(uint32(len(data)))
	f.Write(data)
}

var (
	testSPSPPS = []byte{0, 0, 0, 1, 0x67, 0x42, 0xc0, 0x1f, 0, 0, 0, 1, 0x68, 0xce}
	testIDR    = []byte{0, 0, 0, 1, 0x65, 0x88}
	testP      = []byte{0, 0, 0, 1, 0x41, 0x9a}
)

func collect(t *testing.T, r io.Reader) ([]media.Event, error) {
	t.Helper()
	var evs []media.Event
	err := readVideo(r, func(e media.Event) bool { evs = append(evs, e); return true })
	return evs, err
}

func TestReadVideo(t *testing.T) {
	var f fakeVideo
	f.u32(codecH264)
	f.session(1080, 2400)
	f.packet(flagConfig, testSPSPPS)
	f.packet(flagKeyFrame|1000, testIDR)
	f.packet(17666, testP)
	f.session(2400, 1080) // rotação

	evs, err := collect(t, &f)
	if err != io.EOF {
		t.Fatalf("err = %v, esperava EOF", err)
	}
	if len(evs) != 5 {
		t.Fatalf("esperava 5 eventos, veio %d", len(evs))
	}
	if s := evs[0].Session; s == nil || s.Width != 1080 || s.Height != 2400 {
		t.Fatalf("sessão errada: %+v", evs[0])
	}
	if c := evs[1].Config; c == nil || c.Codec != "avc1.42C01F" {
		t.Fatalf("config errada: %+v", evs[1])
	}
	key := evs[2].Packet
	if key == nil || !key.Key || key.PTS != 1000 || !bytes.Equal(key.Data, append(append([]byte{}, testSPSPPS...), testIDR...)) {
		t.Fatalf("keyframe errado: %+v", key)
	}
	if p := evs[3].Packet; p == nil || p.Key || p.PTS != 17666 || !bytes.Equal(p.Data, testP) {
		t.Fatalf("frame P errado: %+v", p)
	}
	if s := evs[4].Session; s == nil || s.Width != 2400 {
		t.Fatalf("rotação não detectada: %+v", evs[4])
	}
}

func TestReadVideoErrors(t *testing.T) {
	var f fakeVideo
	f.u32(1)
	if _, err := collect(t, &f); err == nil {
		t.Fatal("esperava erro de configuração")
	}
	f.Reset()
	f.u32(codecH264)
	f.u64(0)
	f.u32(maxPacketSize + 1)
	if _, err := collect(t, &f); err == nil {
		t.Fatal("esperava erro de tamanho")
	}
}

func TestReadDeviceMessages(t *testing.T) {
	var b bytes.Buffer
	b.Write([]byte{deviceMsgClipboard, 0, 0, 0, 3, 'a', 'b', 'c'})
	b.Write([]byte{deviceMsgAckClipboard, 0, 0, 0, 0, 0, 0, 0, 7})
	b.Write([]byte{deviceMsgUhidOutput, 0, 1, 0, 2, 0xaa, 0xbb})
	b.Write([]byte{deviceMsgClipboard, 0, 0, 0, 1, 'z'})
	var got []string
	err := readDeviceMessages(&b, func(s string) { got = append(got, s) })
	if err != io.EOF || len(got) != 2 || got[0] != "abc" || got[1] != "z" {
		t.Fatalf("err=%v got=%q", err, got)
	}
}

func FuzzReadVideo(f *testing.F) {
	var v fakeVideo
	v.u32(codecH264)
	v.session(720, 1280)
	v.packet(flagConfig, testSPSPPS)
	v.packet(flagKeyFrame, testIDR)
	f.Add(v.Bytes())
	f.Fuzz(func(t *testing.T, b []byte) {
		readVideo(bytes.NewReader(b), func(media.Event) bool { return true })
		readDeviceMessages(bytes.NewReader(b), nil)
	})
}
