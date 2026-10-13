package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFirstFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "adb")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nao-existe")

	// Pastas e caminhos inexistentes são pulados; vale o primeiro arquivo.
	if got, ok := firstFile(missing, dir, file); !ok || got != file {
		t.Errorf("firstFile = %q, %v; esperado %q, true", got, ok, file)
	}
	if got, ok := firstFile(missing, dir); ok {
		t.Errorf("firstFile = %q, true; esperado nada", got)
	}
	if _, ok := firstFile(); ok {
		t.Error("firstFile sem caminhos devolveu true")
	}
}

func TestFindADBExplicit(t *testing.T) {
	if got, err := FindADB("/caminho/adb"); err != nil || got != "/caminho/adb" {
		t.Errorf("FindADB explícito = %q, %v", got, err)
	}
	t.Setenv("ADB", "/da/variavel/adb")
	if got, err := FindADB(""); err != nil || got != "/da/variavel/adb" {
		t.Errorf("FindADB com ADB = %q, %v", got, err)
	}
}
