//go:build windows

package platform

import (
	"errors"
	"runtime"
	"sync"
	"syscall"
)

const (
	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002
)

var (
	awakeOnce sync.Once
	awakeReq  chan bool
	awakeRes  chan error
)

// KeepAwake impede (on) ou volta a permitir (off) que o PC apague a tela ou
// durma por inatividade: quem só assiste ao espelhamento não mexe no mouse.
func KeepAwake(on bool) error {
	awakeOnce.Do(func() {
		awakeReq, awakeRes = make(chan bool), make(chan error)
		go awakeLoop()
	})
	awakeReq <- on
	return <-awakeRes
}

// awakeLoop fica presa a uma thread do sistema: o SetThreadExecutionState vale
// para a thread que o chamou e só ela consegue desfazer o pedido.
func awakeLoop() {
	runtime.LockOSThread()
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")
	for on := range awakeReq {
		flags := uintptr(esContinuous)
		if on {
			flags |= esSystemRequired | esDisplayRequired
		}
		if prev, _, _ := proc.Call(flags); prev == 0 {
			awakeRes <- errors.New("SetThreadExecutionState falhou")
			continue
		}
		awakeRes <- nil
	}
}
