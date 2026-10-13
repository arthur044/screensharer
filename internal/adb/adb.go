// Package adb chama o executável adb para listar aparelhos, parear, conectar
// pelo Wi-Fi, enviar arquivos e abrir túneis.
package adb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/arthur044/screensharer/internal/platform"
)

// Client executa comandos com um adb específico.
type Client struct {
	Path string
}

// Device é uma linha de `adb devices -l`.
type Device struct {
	Serial string `json:"serial"`
	State  string `json:"state"` // device, unauthorized, offline, no permissions...
	Model  string `json:"model"`
	WiFi   bool   `json:"wifi"`
}

// Command monta um comando adb (sem console visível no Windows).
func (c *Client) Command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.Path, args...)
	platform.HideWindow(cmd)
	return cmd
}

// Run executa e devolve stdout; em erro, inclui o stderr na mensagem.
func (c *Client) Run(ctx context.Context, args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := c.Command(ctx, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String() + " " + out.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("adb %s: %s", strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

func (c *Client) runTimeout(d time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return c.Run(ctx, args...)
}

// Version devolve a primeira linha de `adb version`.
func (c *Client) Version() (string, error) {
	out, err := c.runTimeout(10*time.Second, "version")
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(line), nil
}

// Devices lista os aparelhos conhecidos pelo servidor adb.
func (c *Client) Devices() ([]Device, error) {
	out, err := c.runTimeout(15*time.Second, "devices", "-l")
	if err != nil {
		return nil, err
	}
	return ParseDevices(out), nil
}

// ParseDevices interpreta a saída de `adb devices -l`.
func ParseDevices(out string) []Device {
	var devs []Device
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		serial, rest, ok := strings.Cut(line, "\t")
		if !ok {
			// Algumas versões separam com espaços.
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			serial, rest = f[0], strings.Join(f[1:], " ")
		}
		rest = strings.TrimSpace(rest)
		d := Device{Serial: serial, WiFi: IsWiFiSerial(serial)}
		// O estado pode ter espaços ("no permissions (...)"), então pega até o primeiro "chave:valor".
		var state []string
		for _, f := range strings.Fields(rest) {
			if k, v, ok := strings.Cut(f, ":"); ok && isDeviceField(k) {
				if k == "model" {
					d.Model = strings.ReplaceAll(v, "_", " ")
				}
				continue
			}
			state = append(state, f)
		}
		d.State = strings.Join(state, " ")
		if strings.HasPrefix(d.State, "no permissions") {
			d.State = "no permissions"
		}
		devs = append(devs, d)
	}
	return devs
}

func isDeviceField(k string) bool {
	switch k {
	case "usb", "product", "model", "device", "transport_id":
		return true
	}
	return false
}

// IsWiFiSerial diz se o serial é de uma conexão por rede (ip:porta ou mDNS).
func IsWiFiSerial(serial string) bool {
	return strings.Contains(serial, "._adb-tls-connect.") || hostPortRe.MatchString(serial)
}

var hostPortRe = regexp.MustCompile(`:\d+$`)

// Pair pareia com o código de 6 dígitos mostrado em "Parear dispositivo com código".
func (c *Client) Pair(hostPort, code string) error {
	out, err := c.runTimeout(30*time.Second, "pair", hostPort, code)
	if err != nil {
		return err
	}
	if !strings.Contains(out, "Successfully paired") {
		return errors.New(strings.TrimSpace(out))
	}
	return nil
}

// Connect conecta pelo Wi-Fi. O adb devolve código 0 mesmo quando falha,
// por isso a saída é conferida.
func (c *Client) Connect(hostPort string) error {
	out, err := c.runTimeout(20*time.Second, "connect", hostPort)
	if err != nil {
		return err
	}
	if strings.Contains(out, "connected to") && !strings.Contains(out, "cannot") && !strings.Contains(out, "failed") {
		return nil
	}
	return errors.New(strings.TrimSpace(out))
}

// Disconnect encerra uma conexão Wi-Fi.
func (c *Client) Disconnect(hostPort string) error {
	_, err := c.runTimeout(10*time.Second, "disconnect", hostPort)
	return err
}

// MDNSService é um aparelho anunciado na rede pela depuração sem fio.
type MDNSService struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // "connect" ou "pairing"
	Address string `json:"address"`
}

// MDNSServices lista os aparelhos com depuração sem fio visíveis na rede.
func (c *Client) MDNSServices() ([]MDNSService, error) {
	out, err := c.runTimeout(10*time.Second, "mdns", "services")
	if err != nil {
		return nil, err
	}
	return ParseMDNS(out), nil
}

// ParseMDNS interpreta a saída de `adb mdns services`.
func ParseMDNS(out string) []MDNSService {
	var svcs []MDNSService
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		var typ string
		switch {
		case strings.HasPrefix(f[1], "_adb-tls-connect."):
			typ = "connect"
		case strings.HasPrefix(f[1], "_adb-tls-pairing."):
			typ = "pairing"
		default:
			continue
		}
		svcs = append(svcs, MDNSService{Name: f[0], Type: typ, Address: f[2]})
	}
	return svcs
}

// findConnectAddr procura, entre os serviços mDNS, o endereço de conexão do
// aparelho: primeiro pelo IP, depois pelo serial no nome ("adb-SERIAL-xxxx").
func findConnectAddr(svcs []MDNSService, serial, ip string) (string, bool) {
	byName := ""
	for _, s := range svcs {
		if s.Type != "connect" {
			continue
		}
		if host, _, err := net.SplitHostPort(s.Address); err == nil && host == ip {
			return s.Address, true
		}
		if byName == "" && strings.HasPrefix(s.Name, "adb-"+serial+"-") {
			byName = s.Address
		}
	}
	return byName, byName != ""
}

// Shell executa um comando curto no aparelho e devolve a saída sem espaços nas pontas.
func (c *Client) Shell(serial string, args ...string) (string, error) {
	out, err := c.runTimeout(15*time.Second, append([]string{"-s", serial, "shell"}, args...)...)
	return strings.TrimSpace(out), err
}

// SDK devolve o nível de API do Android (30 = Android 11).
func (c *Client) SDK(serial string) (int, error) {
	out, err := c.Shell(serial, "getprop", "ro.build.version.sdk")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// Push envia um arquivo para o aparelho.
func (c *Client) Push(serial, local, remote string) error {
	_, err := c.runTimeout(60*time.Second, "-s", serial, "push", local, remote)
	return err
}

// Forward cria um túnel de uma porta TCP livre do PC para um socket do aparelho
// e devolve a porta escolhida pelo adb.
func (c *Client) Forward(serial, remote string) (int, error) {
	out, err := c.runTimeout(15*time.Second, "-s", serial, "forward", "tcp:0", remote)
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("porta inesperada do adb forward: %q", out)
	}
	return port, nil
}

// RemoveForward desfaz o túnel.
func (c *Client) RemoveForward(serial string, port int) error {
	_, err := c.runTimeout(10*time.Second, "-s", serial, "forward", "--remove", "tcp:"+strconv.Itoa(port))
	return err
}

// ErrNeedsPairing indica que o aparelho ligou a depuração sem fio, mas este PC
// ainda não está pareado com ele por TLS.
var ErrNeedsPairing = errors.New("é preciso parear este PC com o celular")

// WiFiResult é o resultado de EnableWiFi.
type WiFiResult struct {
	IP      string `json:"ip"`
	Address string `json:"address"` // ip:porta da depuração sem fio
}

var inetRe = regexp.MustCompile(`inet (\d+\.\d+\.\d+\.\d+)/`)

// EnableWiFi liga a depuração sem fio (Android 11+) num aparelho conectado
// pelo USB e tenta conectar nela. Se a conexão exigir pareamento, devolve o IP
// junto com ErrNeedsPairing para a interface abrir o formulário de pareamento.
func (c *Client) EnableWiFi(serial string) (WiFiResult, error) {
	var res WiFiResult
	sdk, err := c.SDK(serial)
	if err != nil {
		return res, err
	}
	if sdk < 30 {
		return res, fmt.Errorf("a depuração sem fio exige Android 11 ou mais novo (este aparelho tem API %d)", sdk)
	}
	out, err := c.Shell(serial, "ip", "-o", "-4", "addr", "show", "wlan0")
	if m := inetRe.FindStringSubmatch(out); err == nil && m != nil {
		res.IP = m[1]
	} else {
		return res, errors.New("o celular não está conectado a uma rede Wi-Fi")
	}
	if _, err := c.Shell(serial, "settings", "put", "global", "adb_wifi_enabled", "1"); err != nil {
		return res, fmt.Errorf("não foi possível ligar a depuração sem fio: %w", err)
	}
	// A porta é aleatória e aparece alguns instantes depois de ligar. Alguns
	// aparelhos (Samsung com Android 13) não deixam o shell ler a propriedade
	// service.adb.tls.port; neles a porta vem do anúncio mDNS.
	for i := 0; i < 20 && res.Address == ""; i++ {
		if i > 0 {
			time.Sleep(250 * time.Millisecond)
		}
		if p, _ := c.Shell(serial, "getprop", "service.adb.tls.port"); p != "" && p != "0" {
			res.Address = res.IP + ":" + p
		} else if svcs, err := c.MDNSServices(); err == nil {
			res.Address, _ = findConnectAddr(svcs, serial, res.IP)
		}
	}
	if res.Address == "" {
		return res, errors.New("a depuração sem fio não informou a porta; ative-a em Opções do desenvolvedor")
	}
	if err := c.Connect(res.Address); err != nil {
		return res, fmt.Errorf("%w (%v)", ErrNeedsPairing, err)
	}
	return res, nil
}
