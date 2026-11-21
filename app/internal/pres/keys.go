package pres

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/koulaxizis/waymark/app/internal/proto"
)

// Key is a logical key; each OS maps it to its own code.
type Key int

const (
	KeyRight Key = iota
	KeyLeft
	KeyB
	KeyEsc
	KeyF5
)

// keyDriver drives an application with keystrokes. It cannot read the slide
// number and cannot jump to a slide.
type keyDriver struct {
	name    string
	mu      sync.Mutex
	pid     int // 0: send to the foreground window
	state   string
	cmd     *exec.Cmd
	start   func() error // starts the show
	onClose func()
}

func (k *keyDriver) Name() string { return k.name }

func (k *keyDriver) send(key Key) error {
	k.mu.Lock()
	pid := k.pid
	k.mu.Unlock()
	return sendKey(pid, key)
}

func (k *keyDriver) Start(from int) error {
	if from > 1 {
		return Errf(proto.ErrUnsupported, "this application cannot start from a given slide; start and press Next")
	}
	if err := k.start(); err != nil {
		return err
	}
	k.mu.Lock()
	k.state = proto.StatusShowing
	k.mu.Unlock()
	return nil
}

func (k *keyDriver) Next() error { return k.send(KeyRight) }
func (k *keyDriver) Prev() error { return k.send(KeyLeft) }
func (k *keyDriver) Goto(int) error {
	return Errf(proto.ErrUnsupported, "this application cannot jump to a slide; open the file with PowerPoint or the built-in viewer")
}

func (k *keyDriver) Black(on bool) error {
	if err := k.send(KeyB); err != nil {
		return err
	}
	k.mu.Lock()
	if on {
		k.state = proto.StatusBlack
	} else {
		k.state = proto.StatusShowing
	}
	k.mu.Unlock()
	return nil
}

func (k *keyDriver) End() error {
	err := k.send(KeyEsc)
	k.mu.Lock()
	k.state = proto.StatusOpen
	k.mu.Unlock()
	return err
}

func (k *keyDriver) Close() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cmd != nil && k.cmd.Process != nil {
		k.cmd.Process.Kill()
		k.cmd.Wait()
	}
	if k.onClose != nil {
		k.onClose()
	}
	return nil
}

func (k *keyDriver) Status() Status {
	k.mu.Lock()
	defer k.mu.Unlock()
	return Status{State: k.state}
}

// Impress drives LibreOffice Impress with its own profile inside the Agent
// folder, so the presenter's LibreOffice settings stay untouched.
func Impress(profileDir string) Opener {
	return Opener{
		Name: "impress",
		Exts: []string{".odp", ".pptx", ".ppt", ".pdf"},
		Open: func(path string) (Driver, error) {
			bin := findSoffice()
			if bin == "" {
				return nil, Errf(proto.ErrAppNotFound, "LibreOffice is not installed on this laptop")
			}
			k := &keyDriver{name: "impress", state: proto.StatusOpen}
			k.start = func() error {
				k.mu.Lock()
				defer k.mu.Unlock()
				if k.cmd != nil && k.cmd.Process != nil {
					return sendKey(k.pid, KeyF5)
				}
				prof := "file:///" + filepath.ToSlash(profileDir)
				cmd := exec.Command(bin, "--nologo", "--norestore", "--nodefault",
					"-env:UserInstallation="+prof, "--show", path)
				if err := cmd.Start(); err != nil {
					return Errf(proto.ErrAppNotFound, "LibreOffice did not start: "+err.Error())
				}
				k.cmd, k.pid = cmd, cmd.Process.Pid
				return nil
			}
			return k, nil
		},
	}
}

func findSoffice() string {
	names := []string{"soffice", "libreoffice"}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	var cands []string
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
			if d := os.Getenv(env); d != "" {
				cands = append(cands, filepath.Join(d, "LibreOffice", "program", "soffice.exe"))
			}
		}
	case "darwin":
		cands = append(cands, "/Applications/LibreOffice.app/Contents/MacOS/soffice")
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// SystemViewer opens the file with the default application of the OS and
// drives it with keystrokes. It is the last fallback.
func SystemViewer() Opener {
	return Opener{
		Name: "system",
		Exts: []string{".pdf", ".pptx", ".ppt", ".odp", ".key"},
		Open: func(path string) (Driver, error) {
			if err := openDefault(path); err != nil {
				return nil, Errf(proto.ErrAppNotFound, "no application is set to open this file")
			}
			k := &keyDriver{name: "system", state: proto.StatusOpen}
			k.start = func() error { return sendKey(0, KeyF5) }
			return k, nil
		},
	}
}
