//go:build windows

package platform

import (
	"os/exec"
	"syscall"
)

// HideWindow evita que cada chamada ao adb abra uma janela de console no Windows.
func HideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
