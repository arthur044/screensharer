package scrcpy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/arthur044/screensharer/internal/media"
)

// Flags do cabeçalho de 12 bytes do socket de vídeo
// (server/.../device/Streamer.java no scrcpy v5.0.1).
const (
	flagSession  = uint64(1) << 63
	flagConfig   = uint64(1) << 62
	flagKeyFrame = uint64(1) << 61
	ptsMask      = flagKeyFrame - 1
)

const (
	codecH264 = 0x68323634 // "h264"
	codecRaw  = 0x00726177 // "raw": PCM de 16 bits, 48 kHz, estéreo (audio/AudioConfig.java)

	// Limite de segurança para um pacote vindo do socket.
	maxPacketSize = 16 << 20
)

// readVideo lê o socket de vídeo depois do nome do aparelho: o codec id e,
// em seguida, pacotes de sessão e de mídia. Cada evento vai para emit.
// O pacote de configuração (SPS/PPS) é colado na frente do pacote seguinte,
// como faz o packet_merger do cliente oficial, para o decoder receber um
// keyframe autossuficiente.
func readVideo(r io.Reader, emit func(media.Event) bool) error {
	var hdr [12]byte
	if _, err := io.ReadFull(r, hdr[:4]); err != nil {
		return fmt.Errorf("lendo codec: %w", err)
	}
	switch id := binary.BigEndian.Uint32(hdr[:4]); id {
	case codecH264:
	case 0:
		return errors.New("o celular desativou o vídeo")
	case 1:
		return errors.New("erro de configuração no servidor do celular (veja o log)")
	default:
		return fmt.Errorf("codec inesperado 0x%08x", id)
	}

	var pendingConfig []byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return err
		}
		ptsFlags := binary.BigEndian.Uint64(hdr[:8])
		if ptsFlags&flagSession != 0 {
			w := binary.BigEndian.Uint32(hdr[4:8])
			h := binary.BigEndian.Uint32(hdr[8:12])
			if !emit(media.Event{Session: &media.Session{Width: int(w), Height: int(h)}}) {
				return nil
			}
			continue
		}
		size := binary.BigEndian.Uint32(hdr[8:12])
		if size == 0 || size > maxPacketSize {
			return fmt.Errorf("tamanho de pacote inválido: %d", size)
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(r, data); err != nil {
			return err
		}
		if ptsFlags&flagConfig != 0 {
			pendingConfig = data
			if codec, ok := media.H264Codec(data); ok {
				if !emit(media.Event{Config: &media.Config{Codec: codec}}) {
					return nil
				}
			}
			continue
		}
		if pendingConfig != nil {
			data = append(pendingConfig, data...)
			pendingConfig = nil
		}
		pkt := &media.Packet{
			Data: data,
			PTS:  int64(ptsFlags & ptsMask),
			Key:  ptsFlags&flagKeyFrame != 0,
		}
		if !emit(media.Event{Packet: pkt}) {
			return nil
		}
	}
}

// errAudioDisabled: o celular não conseguiu capturar o áudio (Android anterior
// ao 11, ou captura recusada). O vídeo continua normalmente.
var errAudioDisabled = errors.New("o celular desativou o áudio")

// readAudio lê o socket de áudio: o codec id e, em seguida, pacotes com o
// mesmo cabeçalho de 12 bytes do vídeo. O codec id só chega depois que a
// captura começa no celular, o que pode levar alguns segundos.
func readAudio(r io.Reader, emit func(*media.Audio) bool) error {
	var hdr [12]byte
	if _, err := io.ReadFull(r, hdr[:4]); err != nil {
		return fmt.Errorf("lendo codec de áudio: %w", err)
	}
	switch id := binary.BigEndian.Uint32(hdr[:4]); id {
	case codecRaw:
	case 0:
		return errAudioDisabled
	case 1:
		return errors.New("erro de configuração do áudio no servidor do celular (veja o log)")
	default:
		return fmt.Errorf("codec de áudio inesperado 0x%08x", id)
	}
	const frameSize = media.AudioChannels * 2
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return err
		}
		ptsFlags := binary.BigEndian.Uint64(hdr[:8])
		size := binary.BigEndian.Uint32(hdr[8:12])
		if size == 0 || size > maxPacketSize {
			return fmt.Errorf("tamanho de pacote de áudio inválido: %d", size)
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(r, data); err != nil {
			return err
		}
		if ptsFlags&flagConfig != 0 || size%frameSize != 0 {
			continue // PCM não tem pacote de configuração
		}
		if !emit(&media.Audio{Data: data, PTS: int64(ptsFlags & ptsMask)}) {
			return nil
		}
	}
}

// Tipos de mensagem do celular para o PC (app/src/device_msg.h).
const (
	deviceMsgClipboard    = 0
	deviceMsgAckClipboard = 1
	deviceMsgUhidOutput   = 2
)

// readDeviceMessages lê o socket de controle no sentido celular → PC.
func readDeviceMessages(r io.Reader, onClipboard func(string)) error {
	var b [9]byte
	for {
		if _, err := io.ReadFull(r, b[:1]); err != nil {
			return err
		}
		switch b[0] {
		case deviceMsgClipboard:
			if _, err := io.ReadFull(r, b[:4]); err != nil {
				return err
			}
			n := binary.BigEndian.Uint32(b[:4])
			if n > clipboardTextMaxLen {
				return fmt.Errorf("clipboard grande demais: %d", n)
			}
			text := make([]byte, n)
			if _, err := io.ReadFull(r, text); err != nil {
				return err
			}
			if onClipboard != nil {
				onClipboard(string(text))
			}
		case deviceMsgAckClipboard:
			if _, err := io.ReadFull(r, b[:8]); err != nil {
				return err
			}
		case deviceMsgUhidOutput:
			if _, err := io.ReadFull(r, b[:4]); err != nil {
				return err
			}
			n := binary.BigEndian.Uint16(b[2:4])
			if _, err := io.CopyN(io.Discard, r, int64(n)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("mensagem do celular desconhecida: %d", b[0])
		}
	}
}
