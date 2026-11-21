//go:build !windows

package wm

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func setupConsole() {}

// IsAdmin reports whether the process runs as root.
func IsAdmin() string {
	if os.Geteuid() == 0 {
		return "yes (root)"
	}
	return "no"
}

// OSVersion returns a short OS description.
func OSVersion() string {
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("sw_vers", "-productVersion").Output(); err == nil {
			return "macOS " + strings.TrimSpace(string(out))
		}
	}
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "PRETTY_NAME=") {
				return strings.Trim(strings.TrimPrefix(l, "PRETTY_NAME="), `"`)
			}
		}
	}
	return runtime.GOOS
}

// OpenFile opens a file with the desktop's default app, if there is one.
func OpenFile(path string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", path).Start()
	default:
		if os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" {
			exec.Command("xdg-open", path).Start()
		}
	}
}
