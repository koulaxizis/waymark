//go:build windows

package wm

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// setupConsole switches the console to UTF-8 so Greek text and QR blocks print.
func setupConsole() {
	k := syscall.NewLazyDLL("kernel32.dll")
	k.NewProc("SetConsoleOutputCP").Call(65001)
	k.NewProc("SetConsoleCP").Call(65001)
}

// IsAdmin reports whether the process token is elevated.
func IsAdmin() string {
	if windows.GetCurrentProcessToken().IsElevated() {
		return "yes (elevated)"
	}
	return "no"
}

// OSVersion returns the Windows build string.
func OSVersion() string {
	maj, min, build := windows.RtlGetNtVersionNumbers()
	return "Windows " + itoa(int(maj)) + "." + itoa(int(min)) + " build " + itoa(int(build))
}

// OpenFile opens a file with its default app (used for the QR image).
func OpenFile(path string) {
	exec.Command("rundll32", "url.dll,FileProtocolHandler", path).Start()
}
