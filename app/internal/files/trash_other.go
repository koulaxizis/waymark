//go:build !windows

package files

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// moveToTrash moves p to the user's Trash: ~/.Trash on macOS, the
// freedesktop.org Trash elsewhere.
func moveToTrash(p string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		return os.Rename(p, uniqueAbs(filepath.Join(home, ".Trash", filepath.Base(p))))
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(home, ".local", "share")
	}
	trash := filepath.Join(base, "Trash")
	if err := os.MkdirAll(filepath.Join(trash, "files"), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(trash, "info"), 0o700); err != nil {
		return err
	}
	dst := uniqueAbs(filepath.Join(trash, "files", filepath.Base(p)))
	name := filepath.Base(dst)
	info := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		(&url.URL{Path: p}).EscapedPath(), time.Now().Format("2006-01-02T15:04:05"))
	infoPath := filepath.Join(trash, "info", name+".trashinfo")
	if err := os.WriteFile(infoPath, []byte(info), 0o600); err != nil {
		return err
	}
	if err := os.Rename(p, dst); err != nil {
		os.Remove(infoPath)
		return err
	}
	return nil
}

func hideDir(string) {} // the leading dot hides it already
