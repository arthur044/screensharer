package scrcpy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/arthur044/screensharer/internal/adb"
	"github.com/arthur044/screensharer/internal/media"
)

// Caminho do servidor no celular. É diferente do usado pelo scrcpy oficial
// para não conflitar se as duas versões estiverem instaladas.
const deviceServerPath = "/data/local/tmp/screensharer-server.jar"

// Options são os parâmetros de captura passados ao servidor.
type Options struct {
	MaxSize   int  // maior lado do vídeo em pixels (0 = resolução do aparelho)
	MaxFPS    int  // 0 = sem limite
	BitRate   int  // bits por segundo
	Control   bool // mouse e teclado do PC controlam o celular
	Audio     bool // o som do celular toca no PC (Android 11+); o celular fica mudo
	StayAwake bool // mantém a tela ligada enquanto conectado pelo USB

	OnClipboard func(text string) // texto copiado no celular (chamado em outra goroutine)
}

// Session é uma conexão ativa com o scrcpy-server num aparelho.
type Session struct {
	DeviceName string

	adb    *adb.Client
	serial string
	port   int
	cmd    *exec.Cmd
	video  net.Conn
	audio  net.Conn
	ctrl   net.Conn
	log    *slog.Logger
	srvOut *lockedBuffer

	events chan media.Event
	done   chan struct{}
	// evMu protege o fechamento de events: o vídeo fecha o canal, e o áudio
	// escreve nele de outra goroutine.
	evMu     sync.RWMutex
	evClosed bool

	// videoStarted fica verdadeiro na primeira sessão de vídeo. Antes disso a
	// captura ainda não existe no servidor, e um RESET_VIDEO o derruba.
	videoStarted atomic.Bool

	mu       sync.Mutex
	err      error
	closed   bool
	clipSeq  uint64
	ctrlLock sync.Mutex
}

// Start envia o servidor, abre o túnel, inicia o servidor e conecta os sockets.
func Start(ctx context.Context, client *adb.Client, serial, serverPath string, opt Options, log *slog.Logger) (*Session, error) {
	s := &Session{
		adb:    client,
		serial: serial,
		log:    log.With("serial", serial),
		srvOut: &lockedBuffer{},
		events: make(chan media.Event, 64),
		done:   make(chan struct{}),
	}
	ok := false
	defer func() {
		if !ok {
			s.Close()
		}
	}()

	if err := client.Push(serial, serverPath, deviceServerPath); err != nil {
		return nil, fmt.Errorf("enviando o servidor para o celular: %w", err)
	}
	scid := rand.Uint32() & 0x7fffffff
	socket := fmt.Sprintf("localabstract:scrcpy_%08x", scid)
	port, err := client.Forward(serial, socket)
	if err != nil {
		return nil, fmt.Errorf("criando o túnel adb: %w", err)
	}
	s.port = port

	args := []string{"-s", serial, "shell",
		"CLASSPATH=" + deviceServerPath, "app_process", "/", "com.genymobile.scrcpy.Server", ServerVersion,
		fmt.Sprintf("scid=%08x", scid),
		"log_level=info",
		"tunnel_forward=true",
		"audio=" + strconv.FormatBool(opt.Audio),
		"video_codec=h264",
		"control=" + strconv.FormatBool(opt.Control),
	}
	if opt.MaxSize > 0 {
		args = append(args, "max_size="+strconv.Itoa(opt.MaxSize))
	}
	if opt.MaxFPS > 0 {
		args = append(args, "max_fps="+strconv.Itoa(opt.MaxFPS))
	}
	if opt.BitRate > 0 {
		args = append(args, "video_bit_rate="+strconv.Itoa(opt.BitRate))
	}
	if opt.StayAwake {
		args = append(args, "stay_awake=true")
	}
	if opt.Audio {
		// PCM sem compressão: não depende de decodificador de áudio na webview.
		args = append(args, "audio_codec=raw")
	}
	s.cmd = client.Command(context.Background(), args...)
	s.cmd.Stdout, s.cmd.Stderr = s.srvOut, s.srvOut
	s.log.Debug("iniciando servidor", "args", strings.Join(args, " "))
	if err := s.cmd.Start(); err != nil {
		return nil, fmt.Errorf("iniciando o servidor no celular: %w", err)
	}
	exited := make(chan struct{})
	go func() { s.cmd.Wait(); close(exited) }()

	// Com o túnel "forward", conectar sempre funciona (quem aceita é o adb).
	// O servidor manda um byte quando está pronto; até lá, tenta de novo.
	addr := "127.0.0.1:" + strconv.Itoa(port)
	for attempt := 0; ; attempt++ {
		select {
		case <-exited:
			return nil, fmt.Errorf("o servidor do celular encerrou: %s", s.serverOutput())
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			var b [1]byte
			if _, err = io.ReadFull(conn, b[:]); err == nil {
				conn.SetReadDeadline(time.Time{})
				s.video = conn
				break
			}
			conn.Close()
		}
		if attempt >= 100 {
			return nil, fmt.Errorf("o servidor do celular não respondeu: %s", s.serverOutput())
		}
		time.Sleep(100 * time.Millisecond)
	}
	// A ordem das conexões é fixa: vídeo, áudio, controle.
	if opt.Audio {
		if s.audio, err = net.DialTimeout("tcp", addr, 5*time.Second); err != nil {
			return nil, fmt.Errorf("conectando o canal de áudio: %w", err)
		}
	}
	if opt.Control {
		if s.ctrl, err = net.DialTimeout("tcp", addr, 5*time.Second); err != nil {
			return nil, fmt.Errorf("conectando o canal de controle: %w", err)
		}
		if tc, ok := s.ctrl.(*net.TCPConn); ok {
			tc.SetNoDelay(true)
		}
	}

	var name [64]byte
	s.video.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.ReadFull(s.video, name[:]); err != nil {
		return nil, fmt.Errorf("lendo o nome do aparelho: %w (%s)", err, s.serverOutput())
	}
	s.video.SetReadDeadline(time.Time{})
	s.DeviceName = string(bytes.TrimRight(name[:], "\x00"))

	go s.readLoop()
	if s.audio != nil {
		go s.audioLoop()
	}
	if s.ctrl != nil {
		go func() {
			err := readDeviceMessages(s.ctrl, opt.OnClipboard)
			if err != nil && !s.isClosed() {
				s.log.Warn("canal de controle encerrado", "err", err)
			}
		}()
	}
	go func() {
		<-exited
		if !s.isClosed() {
			s.log.Warn("servidor do celular encerrou", "saida", s.serverOutput())
		}
	}()
	ok = true
	return s, nil
}

// audioLoop repassa o áudio como eventos. Se a fila estiver cheia o bloco é
// descartado: áudio atrasado é pior que um estalo, e o vídeo não pode esperar.
func (s *Session) audioLoop() {
	err := readAudio(s.audio, func(a *media.Audio) bool {
		s.evMu.RLock()
		defer s.evMu.RUnlock()
		if s.evClosed {
			return false
		}
		select {
		case s.events <- media.Event{Audio: a}:
		default:
		}
		return true
	})
	if err != nil && !s.isClosed() {
		s.log.Warn("áudio encerrado", "err", err)
	}
}

func (s *Session) readLoop() {
	defer func() {
		s.evMu.Lock()
		s.evClosed = true
		close(s.events)
		s.evMu.Unlock()
	}()
	err := readVideo(s.video, func(e media.Event) bool {
		if e.Session != nil {
			s.videoStarted.Store(true)
		}
		select {
		case s.events <- e:
			return true
		case <-s.done:
			return false
		}
	})
	if s.isClosed() {
		return
	}
	if errors.Is(err, io.EOF) {
		err = errors.New("o celular encerrou a transmissão (desconectado ou tela bloqueada pelo sistema)")
	}
	if out := s.serverOutput(); out != "" {
		err = fmt.Errorf("%w — log do servidor: %s", err, out)
	}
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

// Events entrega sessões, configurações, pacotes de vídeo e blocos de áudio.
func (s *Session) Events() <-chan media.Event { return s.events }

// Err diz por que a sessão terminou.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Send escreve uma mensagem de controle já serializada.
func (s *Session) Send(msg []byte) error {
	if s.ctrl == nil {
		return nil
	}
	s.ctrlLock.Lock()
	defer s.ctrlLock.Unlock()
	s.ctrl.SetWriteDeadline(time.Now().Add(time.Second))
	_, err := s.ctrl.Write(msg)
	return err
}

// RequestKeyframe reinicia o encoder do celular para receber um keyframe já.
// Antes do vídeo começar não faz nada: o primeiro pacote já é um keyframe, e o
// scrcpy-server v5.0.1 morre (NullPointerException em Controller.resetVideo)
// se o pedido chegar antes de a captura ser criada.
func (s *Session) RequestKeyframe() {
	if !s.videoStarted.Load() {
		return
	}
	if err := s.Send(ResetVideo()); err != nil {
		s.log.Debug("pedido de keyframe falhou", "err", err)
	}
}

// SetClipboard copia texto para o celular e, com paste, cola no campo em foco.
func (s *Session) SetClipboard(text string, paste bool) error {
	s.mu.Lock()
	s.clipSeq++
	seq := s.clipSeq
	s.mu.Unlock()
	return s.Send(SetClipboard(seq, text, paste))
}

// HandleInput converte um evento da interface em mensagem do scrcpy.
func (s *Session) HandleInput(in media.Input) error {
	pos := Position{X: int32(in.X), Y: int32(in.Y), ScreenW: uint16(in.W), ScreenH: uint16(in.H)}
	switch in.T {
	case "touch":
		pressure := float32(1)
		if in.Action == ActionUp {
			pressure = 0
		}
		return s.Send(InjectTouch(byte(in.Action), PointerIDMouse, pos, pressure, in.ABtn, in.Btn))
	case "scroll":
		return s.Send(InjectScroll(pos, in.HS, in.VS, in.Btn))
	case "key":
		return s.Send(InjectKeycode(byte(in.Action), in.Code, in.Repeat, in.Meta))
	case "text":
		return s.Send(InjectText(in.S))
	case "back":
		return s.Send(BackOrScreenOn(byte(in.Action)))
	case "copy":
		return s.Send([]byte{msgGetClipboard, 1}) // SC_COPY_KEY_COPY
	case "cmd":
		switch in.S {
		case "rotate":
			return s.Send(RotateDevice())
		case "notifications":
			return s.Send(ExpandNotificationPanel())
		case "collapse":
			return s.Send(CollapsePanels())
		case "screen_off":
			return s.Send(SetDisplayPower(false))
		case "screen_on":
			return s.Send(SetDisplayPower(true))
		case "keyframe":
			s.RequestKeyframe()
			return nil
		}
	}
	return fmt.Errorf("entrada desconhecida: %q %q", in.T, in.S)
}

// Close encerra os sockets, o servidor e o túnel. Pode ser chamado mais de uma vez.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	close(s.done)
	if s.video != nil {
		s.video.Close()
	}
	if s.audio != nil {
		s.audio.Close()
	}
	if s.ctrl != nil {
		s.ctrl.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		// Fechar os sockets já faz o servidor sair; o kill garante o adb shell.
		time.AfterFunc(500*time.Millisecond, func() { s.cmd.Process.Kill() })
	}
	if s.port != 0 {
		s.adb.RemoveForward(s.serial, s.port)
	}
	return nil
}

func (s *Session) serverOutput() string {
	out := strings.TrimSpace(s.srvOut.String())
	if len(out) > 2000 {
		out = "…" + out[len(out)-2000:]
	}
	return out
}

// lockedBuffer guarda a saída do servidor para mostrar em mensagens de erro.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.b.Len() > 64<<10 {
		l.b.Reset()
	}
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
