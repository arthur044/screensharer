//go:build !windows

package platform

// KeepAwake impede (on) ou volta a permitir (off) que o PC apague ou bloqueie a
// tela por inatividade: quem só assiste ao espelhamento não mexe no mouse.
// No Linux só o Hyprland é atendido; nos outros ambientes não faz nada.
func KeepAwake(on bool) error {
	if !OnHyprland() {
		return nil
	}
	return hyprIdleInhibit(on)
}
