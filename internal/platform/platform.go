// Package platform concentra o que muda entre Windows e Linux.
package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Exe é o nome de um executável com a extensão do sistema.
func Exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// FindADB procura o adb: caminho explícito, variável ADB, ao lado do app,
// PATH e as pastas padrão do Android SDK.
func FindADB(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if p := os.Getenv("ADB"); p != "" {
		return p, nil
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, Exe("adb")),
			filepath.Join(dir, "platform-tools", Exe("adb")))
	}
	if p, err := exec.LookPath("adb"); err == nil {
		return p, nil
	}
	for _, env := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if v := os.Getenv(env); v != "" {
			candidates = append(candidates, filepath.Join(v, "platform-tools", Exe("adb")))
		}
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			candidates = append(candidates, filepath.Join(v, "Android", "Sdk", "platform-tools", "adb.exe"))
		}
	default:
		candidates = append(candidates, filepath.Join(home, "Android", "Sdk", "platform-tools", "adb"))
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", exec.ErrNotFound
}

// DataDir é a pasta onde o app guarda o scrcpy-server baixado e as configurações.
func DataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "screensharer")
	return dir, os.MkdirAll(dir, 0o755)
}
