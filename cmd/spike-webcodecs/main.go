// Spike da Fase 0: gera H.264 com o ffmpeg, separa as access units e envia por WebSocket
// para o navegador decodificar com WebCodecs. Serve para medir o pipeline, não é o produto.
package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"io"
	"log"
	"net/http"
	"os/exec"

	"github.com/coder/websocket"
)

//go:embed index.html
var page []byte

// splitAU lê o stream Annex B e entrega uma access unit por vez, separando nos NALs AUD (tipo 9).
func splitAU(r io.Reader, out chan<- []byte) {
	br := bufio.NewReaderSize(r, 1<<20)
	var buf []byte
	tmp := make([]byte, 64<<10)
	start := []byte{0, 0, 0, 1, 9}
	for {
		n, err := br.Read(tmp)
		buf = append(buf, tmp[:n]...)
		for {
			i := bytes.Index(buf[1:], start)
			if i < 0 {
				break
			}
			au := append([]byte(nil), buf[:i+1]...)
			out <- au
			buf = buf[i+1:]
		}
		if err != nil {
			close(out)
			return
		}
	}
}

func main() {
	const addr = "127.0.0.1:8090"
	log.Printf("abra http://%s no Chrome ou no Edge (precisa do ffmpeg no PATH)", addr)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write(page) })
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		cmd := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=60",
			"-c:v", "libx264", "-profile:v", "baseline", "-tune", "zerolatency", "-preset", "ultrafast", "-g", "600",
			"-x264-params", "aud=1", "-b:v", "8M", "-f", "h264", "-")
		so, _ := cmd.StdoutPipe()
		cmd.Start()
		aus := make(chan []byte, 8)
		go splitAU(so, aus)
		for au := range aus {
			if err := c.Write(ctx, websocket.MessageBinary, au); err != nil {
				return
			}
		}
	})
	log.Fatal(http.ListenAndServe(addr, nil))
}
