// Package stream leva o vídeo do Go até a interface por um WebSocket local
// (127.0.0.1, porta aleatória, token aleatório) e traz a entrada de volta.
//
// Os bindings do Wails trafegam JSON e não servem para 8 Mbps de dados
// binários, por isso o vídeo tem este caminho próprio.
package stream

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/arthur044/screensharer/internal/media"
)

// Tamanho da fila por cliente. Se encher, o cliente está atrasado: os frames
// são descartados até o próximo keyframe, em vez de acumular atraso.
const queueSize = 30

// Server aceita uma conexão da interface por vez.
type Server struct {
	ln    net.Listener
	token string
	log   *slog.Logger

	mu     sync.Mutex
	pump   *pump
	client *client
}

// New abre o listener em 127.0.0.1 numa porta livre.
func New(log *slog.Logger) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := make([]byte, 16)
	rand.Read(b)
	s := &Server{ln: ln, token: hex.EncodeToString(b), log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", s.handle)
	go http.Serve(ln, mux)
	return s, nil
}

// URL é o endereço que a interface usa para conectar (inclui o token).
func (s *Server) URL() string {
	return fmt.Sprintf("ws://%s/stream?token=%s", s.ln.Addr(), s.token)
}

// Close encerra o listener.
func (s *Server) Close() error { return s.ln.Close() }

// Attach passa a transmitir src. onEnd é chamado uma vez quando src termina.
func (s *Server) Attach(src media.Source, onInput func(media.Input), onEnd func(error)) {
	p := &pump{srv: s, src: src, onInput: onInput}
	s.mu.Lock()
	s.pump = p
	s.mu.Unlock()
	go p.run(onEnd)
}

// Detach para de transmitir (a fonte é fechada por quem a criou).
func (s *Server) Detach() {
	s.mu.Lock()
	s.pump = nil
	c := s.client
	s.mu.Unlock()
	if c != nil {
		c.sendJSON(map[string]any{"type": "end", "reason": ""})
	}
}

// allowedOrigin aceita a webview do Wails (Linux: wails://wails; Windows:
// http://wails.localhost) e páginas locais do `wails dev`. O token continua
// obrigatório em todos os casos.
func allowedOrigin(origin string) bool {
	if origin == "" {
		return true // clientes que não são navegador (testes)
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "wails", "wails.localhost", "localhost", "127.0.0.1":
		return true
	}
	return false
}

var upgrader = websocket.Upgrader{
	CheckOrigin:     func(r *http.Request) bool { return allowedOrigin(r.Header.Get("Origin")) },
	WriteBufferSize: 256 << 10,
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(s.token)) != 1 {
		http.Error(w, "token inválido", http.StatusForbidden)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &client{conn: conn, out: make(chan []byte, queueSize), text: make(chan []byte, 16), done: make(chan struct{})}
	s.mu.Lock()
	old := s.client
	s.client = c
	p := s.pump
	s.mu.Unlock()
	if old != nil {
		old.close()
	}
	go c.writeLoop()
	if p != nil {
		p.welcome(c)
	}
	// Loop de leitura: entrada da interface.
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var in media.Input
		if err := json.Unmarshal(msg, &in); err != nil {
			continue
		}
		s.mu.Lock()
		p := s.pump
		s.mu.Unlock()
		if p != nil && p.onInput != nil {
			p.onInput(in)
		}
	}
	s.mu.Lock()
	if s.client == c {
		s.client = nil
	}
	s.mu.Unlock()
	c.close()
}

func (s *Server) current() (*client, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client, s.client != nil
}

// pump lê a fonte o tempo todo (mesmo sem cliente) e guarda a última sessão e
// configuração, para mandar a um cliente que conecte no meio.
type pump struct {
	srv     *Server
	src     media.Source
	onInput func(media.Input)

	mu      sync.Mutex
	session *media.Session
	config  *media.Config
}

func (p *pump) welcome(c *client) {
	p.mu.Lock()
	sess, cfg := p.session, p.config
	p.mu.Unlock()
	if sess != nil {
		c.sendJSON(sessionMsg(sess))
	}
	if cfg != nil {
		c.sendJSON(configMsg(cfg))
	}
	c.waitKey.Store(true)
	p.src.RequestKeyframe()
}

func sessionMsg(s *media.Session) map[string]any {
	return map[string]any{"type": "session", "width": s.Width, "height": s.Height}
}

func configMsg(c *media.Config) map[string]any {
	return map[string]any{"type": "config", "codec": c.Codec}
}

func (p *pump) active() bool {
	p.srv.mu.Lock()
	defer p.srv.mu.Unlock()
	return p.srv.pump == p
}

func (p *pump) run(onEnd func(error)) {
	stats := time.NewTicker(time.Second)
	defer stats.Stop()
	events := p.src.Events()
	for {
		select {
		case e, ok := <-events:
			if !ok {
				err := p.src.Err()
				if p.active() {
					if c, ok := p.srv.current(); ok {
						reason := ""
						if err != nil {
							reason = err.Error()
						}
						c.sendJSON(map[string]any{"type": "end", "reason": reason})
					}
				}
				if onEnd != nil {
					onEnd(err)
				}
				return
			}
			if !p.active() {
				continue // fonte desanexada: só drena até fechar
			}
			p.handle(e)
		case <-stats.C:
			if c, ok := p.srv.current(); ok && p.active() {
				c.sendJSON(map[string]any{"type": "stats", "dropped": c.dropped.Load()})
			}
		}
	}
}

func (p *pump) handle(e media.Event) {
	// Sessão e configuração são guardadas antes de procurar o cliente. Na ordem
	// inversa, um cliente que conectasse entre as duas leituras não receberia a
	// mensagem nem daqui nem do welcome, e ficaria sem decoder. Nesta ordem ele
	// pode recebê-la duas vezes, o que a interface tolera.
	switch {
	case e.Session != nil:
		p.mu.Lock()
		p.session = e.Session
		p.mu.Unlock()
		if c, ok := p.srv.current(); ok {
			c.sendJSON(sessionMsg(e.Session))
		}
		return
	case e.Config != nil:
		p.mu.Lock()
		p.config = e.Config
		p.mu.Unlock()
		if c, ok := p.srv.current(); ok {
			c.sendJSON(configMsg(e.Config))
		}
		return
	}
	c, hasClient := p.srv.current()
	switch {
	case e.Packet != nil && hasClient:
		pkt := e.Packet
		if c.waitKey.Load() {
			if !pkt.Key {
				return
			}
			c.waitKey.Store(false)
		}
		frame := make([]byte, 10+len(pkt.Data))
		frame[0] = 1
		if pkt.Key {
			frame[1] = 1
		}
		binary.BigEndian.PutUint64(frame[2:], uint64(pkt.PTS))
		copy(frame[10:], pkt.Data)
		select {
		case c.out <- frame:
		default:
			// Cliente atrasado: descarta até o próximo keyframe e pede um já.
			c.dropped.Add(1)
			c.waitKey.Store(true)
			p.src.RequestKeyframe()
		}
	}
}

type client struct {
	conn    *websocket.Conn
	out     chan []byte // vídeo (binário)
	text    chan []byte // eventos (JSON)
	done    chan struct{}
	once    sync.Once
	waitKey atomic.Bool
	dropped atomic.Int64
}

func (c *client) sendJSON(v any) {
	b, _ := json.Marshal(v)
	select {
	case c.text <- b:
	case <-c.done:
	case <-time.After(time.Second):
	}
}

func (c *client) writeLoop() {
	defer c.close()
	for {
		var err error
		select {
		case b := <-c.text: // eventos têm prioridade sobre vídeo
			err = c.conn.WriteMessage(websocket.TextMessage, b)
		case b := <-c.out:
			select {
			case t := <-c.text:
				if err = c.conn.WriteMessage(websocket.TextMessage, t); err != nil {
					return
				}
			default:
			}
			err = c.conn.WriteMessage(websocket.BinaryMessage, b)
		case <-c.done:
			return
		}
		if err != nil {
			return
		}
	}
}

func (c *client) close() {
	c.once.Do(func() {
		close(c.done)
		c.conn.Close()
	})
}
