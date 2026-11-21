//go:build !windows

package pres

import (
	"os/exec"
	"runtime"

	"github.com/koulaxizis/waymark/app/internal/proto"
)

// Keystrokes on macOS (Accessibility permission) and Linux X11 come in a later
// version. Wayland does not allow them at all.
func sendKey(int, Key) error {
	return Errf(proto.ErrUnsupported, "keystrokes are not supported on this system yet; use the built-in viewer")
}

func openDefault(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", path).Start()
	}
	return exec.Command("xdg-open", path).Start()
}
