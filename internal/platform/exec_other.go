//go:build !windows

package platform

import "os/exec"

// HideWindow não faz nada fora do Windows.
func HideWindow(*exec.Cmd) {}
