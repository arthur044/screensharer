package scrcpy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Versão fixada do scrcpy-server. O servidor exige que o cliente informe
// exatamente a mesma versão, e o protocolo muda entre versões: ao atualizar,
// confira control.go e demux.go contra o código da nova tag.
const (
	ServerVersion = "5.0.1"
	ServerURL     = "https://github.com/Genymobile/scrcpy/releases/download/v" + ServerVersion + "/scrcpy-server-v" + ServerVersion
	ServerSHA256  = "764eb6f79811d5211fe9df341120882ba9994c7a61b897d7bf3fb662e53bc536"
)

// FindServer devolve o caminho de um scrcpy-server válido. Procura, em ordem:
// o caminho explícito, a variável SCRCPY_SERVER_PATH, ao lado do executável e
// a pasta de dados. Se não achar, baixa para a pasta de dados e confere o SHA-256.
func FindServer(explicit, dataDir string) (string, error) {
	if explicit != "" {
		return explicit, nil // escolhido pelo usuário: não confere o hash
	}
	if p := os.Getenv("SCRCPY_SERVER_PATH"); p != "" {
		return p, nil
	}
	name := "scrcpy-server-v" + ServerVersion
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), name))
	}
	cached := filepath.Join(dataDir, name)
	candidates = append(candidates, cached)
	for _, c := range candidates {
		if ok, _ := checkSHA256(c); ok {
			return c, nil
		}
	}
	if err := download(ServerURL, cached); err != nil {
		return "", fmt.Errorf("baixando o scrcpy-server v%s: %w (você pode baixá-lo manualmente de %s e passar o caminho com --server)", ServerVersion, err, ServerURL)
	}
	return cached, nil
}

func checkSHA256(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	return hex.EncodeToString(h.Sum(nil)) == ServerSHA256, nil
}

func download(url, dst string) error {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 8<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != ServerSHA256 {
		os.Remove(tmp)
		return fmt.Errorf("SHA-256 não confere (recebido %s)", got)
	}
	return os.Rename(tmp, dst)
}
