//go:build !windows

package main

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
)

var awake struct {
	mu  sync.Mutex
	cmd *exec.Cmd
}

// keepAwake stops the display from sleeping during a show: caffeinate on
// macOS, systemd-inhibit on Linux. Both end with the app.
func keepAwake(on bool) {
	awake.mu.Lock()
	defer awake.mu.Unlock()
	if !on {
		if awake.cmd != nil {
			awake.cmd.Process.Kill()
			awake.cmd.Wait()
			awake.cmd = nil
		}
		return
	}
	if awake.cmd != nil {
		return
	}
	pid := strconv.Itoa(os.Getpid())
	var c *exec.Cmd
	if runtime.GOOS == "darwin" {
		c = exec.Command("caffeinate", "-d", "-i", "-w", pid)
	} else if p, err := exec.LookPath("systemd-inhibit"); err == nil {
		c = exec.Command(p, "--what=idle:sleep", "--who=Waymark Remote", "--why=Presentation", "--mode=block", "sleep", "infinity")
	} else {
		return
	}
	if c.Start() == nil {
		awake.cmd = c
	}
}

// moveToProjector: on macOS and Linux the built-in viewer goes full screen
// on the screen the window is on; the presenter drags it to the projector.
func moveToProjector(string) {}
