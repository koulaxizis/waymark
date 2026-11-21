//go:build !windows

package pres

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/koulaxizis/waymark/app/internal/proto"
)

// sendKey presses one key in the presentation window: on macOS through
// System Events (needs the Accessibility permission once), on Linux X11
// through xdotool. Wayland does not let one app type into another; there
// the built-in PDF viewer is the way.
func sendKey(pid int, k Key) error {
	if runtime.GOOS == "darwin" {
		return sendKeyMac(pid, k)
	}
	return sendKeyX11(pid, k)
}

var macCodes = map[Key]int{KeyRight: 124, KeyLeft: 123, KeyB: 11, KeyEsc: 53, KeyF5: 96}

func sendKeyMac(pid int, k Key) error {
	code, ok := macCodes[k]
	if !ok {
		return Errf(proto.ErrUnsupported, "unknown key")
	}
	script := fmt.Sprintf(`tell application "System Events" to key code %d`, code)
	if pid > 0 {
		script = fmt.Sprintf(`tell application "System Events"
set frontmost of (first process whose unix id is %d) to true
key code %d
end tell`, pid, code)
	}
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		s := string(out)
		if strings.Contains(s, "1002") || strings.Contains(s, "-25211") || strings.Contains(s, "-1719") || strings.Contains(s, "not allowed") {
			return Errf(proto.ErrPermission, "allow Waymark Remote in System Settings > Privacy & Security > Accessibility")
		}
		return Errf(proto.ErrIO, strings.TrimSpace(s))
	}
	return nil
}

var x11Keys = map[Key]string{KeyRight: "Right", KeyLeft: "Left", KeyB: "b", KeyEsc: "Escape", KeyF5: "F5"}

func sendKeyX11(pid int, k Key) error {
	if os.Getenv("DISPLAY") == "" {
		return Errf(proto.ErrUnsupported, "this desktop (Wayland) does not allow keystrokes; use the built-in viewer with a PDF")
	}
	name, ok := x11Keys[k]
	if !ok {
		return Errf(proto.ErrUnsupported, "unknown key")
	}
	xd, err := exec.LookPath("xdotool")
	if err != nil {
		return Errf(proto.ErrUnsupported, "install xdotool for keystrokes, or use the built-in viewer with a PDF")
	}
	args := []string{"key", "--clearmodifiers", name}
	if pid > 0 {
		args = []string{"search", "--onlyvisible", "--pid", strconv.Itoa(pid), "windowactivate", "--sync", "key", "--clearmodifiers", name}
	}
	if out, err := exec.Command(xd, args...).CombinedOutput(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && pid > 0 {
			return Errf(proto.ErrAppNotFound, "the presentation window is gone")
		}
		return Errf(proto.ErrIO, strings.TrimSpace(string(out)))
	}
	return nil
}

func openDefault(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", path).Start()
	}
	return exec.Command("xdg-open", path).Start()
}
