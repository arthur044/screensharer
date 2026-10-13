// Package demo é uma fonte de vídeo sintética (ffmpeg testsrc2) para testar
// a janela, o decoder e o desempenho sem celular conectado.
package demo

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/arthur044/screensharer/internal/media"
	"github.com/arthur044/screensharer/internal/platform"
)

// Available diz se o ffmpeg está no PATH.
func Available() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// Source gera H.264 em tempo real com o ffmpeg.
type Source struct {
	events chan media.Event
	ctx    context.Context
	cancel context.CancelFunc
	cmd    *exec.Cmd
	log    *slog.Logger

	mu  sync.Mutex
	err error
}

// Start inicia o ffmpeg com o tamanho e o FPS pedidos.
func Start(width, height, fps, bitRate int, log *slog.Logger) (*Source, error) {
	ctx, cancel := context.WithCancel(context.Background())
	gop := strconv.Itoa(fps) // keyframe a cada 1 s: a fonte de teste não atende pedido de keyframe
	cmd := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error", "-re",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=%d", width, height, fps),
		"-c:v", "libx264", "-profile:v", "baseline", "-tune", "zerolatency", "-preset", "ultrafast",
		"-g", gop, "-x264-params", "aud=1", "-b:v", strconv.Itoa(bitRate), "-f", "h264", "-")
	platform.HideWindow(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("iniciando o ffmpeg: %w", err)
	}
	s := &Source{events: make(chan media.Event, 64), ctx: ctx, cancel: cancel, cmd: cmd, log: log}
	go func() {
		defer close(s.events)
		s.send(media.Event{Session: &media.Session{Width: width, Height: height}})
		err := splitAccessUnits(out, s.emit)
		cmd.Wait()
		if ctx.Err() == nil {
			s.mu.Lock()
			s.err = fmt.Errorf("o ffmpeg parou: %v %s", err, stderr.String())
			s.mu.Unlock()
		}
	}()
	return s, nil
}

// send entrega o evento, ou desiste se a fonte foi fechada.
func (s *Source) send(e media.Event) {
	select {
	case s.events <- e:
	case <-s.ctx.Done():
	}
}

func (s *Source) emit(au []byte) {
	if codec, ok := media.H264Codec(au); ok {
		s.send(media.Event{Config: &media.Config{Codec: codec}})
	}
	s.send(media.Event{Packet: &media.Packet{
		Data: au,
		PTS:  time.Now().UnixMicro(),
		Key:  media.HasIDR(au),
	}})
}

// splitAccessUnits separa o Annex B em access units pelos NALs AUD (tipo 9).
func splitAccessUnits(r io.Reader, emit func([]byte)) error {
	br := bufio.NewReaderSize(r, 1<<20)
	aud := []byte{0, 0, 0, 1, 9}
	var buf []byte
	tmp := make([]byte, 64<<10)
	for {
		n, err := br.Read(tmp)
		buf = append(buf, tmp[:n]...)
		for {
			i := bytes.Index(buf[min(1, len(buf)):], aud)
			if i < 0 {
				break
			}
			emit(append([]byte(nil), buf[:i+1]...))
			buf = buf[i+1:]
		}
		if err != nil {
			if len(buf) > 0 {
				emit(buf)
			}
			return err
		}
	}
}

func (s *Source) Events() <-chan media.Event { return s.events }

func (s *Source) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// RequestKeyframe não é suportado: o próximo keyframe chega pelo GOP (1 s).
func (s *Source) RequestKeyframe() {}

// HandleInput só registra no log, para conferir o mapeamento de entrada.
func (s *Source) HandleInput(in media.Input) error {
	s.log.Debug("entrada (fonte de teste)", "input", in)
	return nil
}

func (s *Source) Close() error {
	s.cancel()
	return nil
}
