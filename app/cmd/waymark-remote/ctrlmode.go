package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/koulaxizis/waymark/app/internal/controller"
	"github.com/koulaxizis/waymark/app/internal/files"
	"github.com/koulaxizis/waymark/app/internal/pair"
	"github.com/koulaxizis/waymark/app/internal/proto"
)

func (a *App) startController() error {
	a.mu.Lock()
	name, role := a.cfg.Name, a.cfg.Role
	a.mu.Unlock()
	c, err := controller.New(name, role, &ctrlUI{app: a})
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.ctrl = c
	if a.cfg.RelayURL != "" {
		c.SetRelay(a.cfg.RelayURL, a.cfg.RelayKey)
	}
	a.mu.Unlock()
	a.logf("Controller ready")
	go a.watchOperatorFolders()
	return nil
}

type ctrlUI struct{ app *App }

func (u *ctrlUI) Codes(c []pair.Code) { u.app.emit("ctrl:codes", c) }
func (u *ctrlUI) SAS(sas, agent string) {
	u.app.emit("ctrl:sas", map[string]string{"sas": sas, "name": agent})
}
func (u *ctrlUI) Agents(l []controller.AgentInfo) { u.app.emit("ctrl:agents", l) }
func (u *ctrlUI) Event(id string, e proto.Event) {
	u.app.emit("ctrl:event", map[string]any{"agent": id, "event": e})
}
func (u *ctrlUI) Progress(p controller.Progress) { u.app.emit("ctrl:progress", p) }
func (u *ctrlUI) Log(format string, args ...any) { u.app.logf(format, args...) }

func (a *App) getCtrl() (*controller.Controller, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctrl == nil {
		return nil, errors.New("the Controller is not running")
	}
	return a.ctrl, nil
}

// CtrlCodes returns the pairing codes to show.
func (a *App) CtrlCodes() ([]pair.Code, error) {
	c, err := a.getCtrl()
	if err != nil {
		return nil, err
	}
	return c.Codes(), nil
}

// CtrlNewCode replaces the code.
func (a *App) CtrlNewCode() {
	if c, err := a.getCtrl(); err == nil {
		c.NewCode()
	}
}

// CtrlAgents lists the paired Agents.
func (a *App) CtrlAgents() ([]controller.AgentInfo, error) {
	c, err := a.getCtrl()
	if err != nil {
		return nil, err
	}
	return c.Agents(), nil
}

// CallResult carries either data or a coded error to the page.
type CallResult struct {
	Data json.RawMessage `json:"data,omitempty"`
	Code string          `json:"code,omitempty"`
	Err  string          `json:"err,omitempty"`
}

func result(raw json.RawMessage, err error) CallResult {
	if err == nil {
		return CallResult{Data: raw}
	}
	var ce *controller.CallError
	if errors.As(err, &ce) {
		return CallResult{Code: ce.Code, Err: ce.Msg}
	}
	return CallResult{Code: "LINK", Err: err.Error()}
}

// CtrlCall sends one command with JSON arguments.
func (a *App) CtrlCall(agentID, cmd string, args json.RawMessage) CallResult {
	c, err := a.getCtrl()
	if err != nil {
		return result(nil, err)
	}
	var v any
	if len(args) > 0 && string(args) != "null" {
		v = args
	}
	return result(c.Call(agentID, cmd, v))
}

// CtrlForget unpairs an Agent.
func (a *App) CtrlForget(agentID string) {
	if c, err := a.getCtrl(); err == nil {
		c.Forget(agentID)
	}
}

// CtrlUploadPick asks for files and sends them into relDir on the Agent.
func (a *App) CtrlUploadPick(agentID, relDir string) error {
	ps, err := wr.OpenMultipleFilesDialog(a.ctx, wr.OpenDialogOptions{Title: "Waymark"})
	if err != nil || len(ps) == 0 {
		return err
	}
	a.CtrlUploadPaths(agentID, ps, relDir)
	return nil
}

// CtrlUploadFolderPick asks for a folder and sends it with its structure.
func (a *App) CtrlUploadFolderPick(agentID, relDir string) error {
	d, err := wr.OpenDirectoryDialog(a.ctx, wr.OpenDialogOptions{Title: "Waymark"})
	if err != nil || d == "" {
		return err
	}
	a.CtrlUploadPaths(agentID, []string{d}, relDir)
	return nil
}

// CtrlUploadPaths sends local files and folders (from a pick or a drop).
// It returns at once; progress arrives as events.
func (a *App) CtrlUploadPaths(agentID string, paths []string, relDir string) {
	c, err := a.getCtrl()
	if err != nil {
		return
	}
	go func() {
		for _, p := range paths {
			st, err := os.Stat(p)
			if err != nil {
				continue
			}
			dst := path.Join(relDir, filepath.Base(p))
			if st.IsDir() {
				err = c.UploadDir(agentID, p, dst)
			} else {
				_, err = c.Upload(agentID, p, dst)
			}
			if err != nil {
				a.logf("upload %s: %v", filepath.Base(p), err)
			}
		}
		a.emit("ctrl:refresh", agentID)
	}()
}

func (a *App) onDrop(x, y int, paths []string) {
	a.emit("ctrl:drop", paths)
}

// CtrlDownload asks where to save and fetches rel (a file or a folder).
func (a *App) CtrlDownload(agentID, rel string, isDir bool) error {
	c, err := a.getCtrl()
	if err != nil {
		return err
	}
	if !isDir {
		dst, err := wr.SaveFileDialog(a.ctx, wr.SaveDialogOptions{Title: "Waymark", DefaultFilename: path.Base(rel)})
		if err != nil || dst == "" {
			return err
		}
		go func() {
			if err := c.Download(agentID, rel, dst); err != nil {
				a.logf("download %s: %v", rel, err)
			}
		}()
		return nil
	}
	dir, err := wr.OpenDirectoryDialog(a.ctx, wr.OpenDialogOptions{Title: "Waymark"})
	if err != nil || dir == "" {
		return err
	}
	raw, err := c.Call(agentID, proto.CmdFileList, nil)
	if err != nil {
		return err
	}
	var l []proto.Entry
	json.Unmarshal(raw, &l)
	base := path.Dir(rel)
	if base == "." {
		base = ""
	}
	go func() {
		for _, e := range l {
			if e.Dir || !strings.HasPrefix(e.Path, rel+"/") {
				continue
			}
			sub := strings.TrimPrefix(e.Path, base)
			sub = strings.TrimPrefix(sub, "/")
			dst := filepath.Join(dir, filepath.FromSlash(sub))
			if err := c.Download(agentID, e.Path, dst); err != nil {
				a.logf("download %s: %v", e.Path, err)
			}
		}
	}()
	return nil
}

// Proposal 6: one folder per Agent next to the exe. Whatever the operator
// drops there in Explorer is sent to the Agent. One way only: deleting here
// never deletes on the presenter laptop.

func operatorRoot() string { return filepath.Join(exeDir(), "Waymark Operator") }

// CtrlOpenOperatorFolder opens the folder of one Agent in the file manager.
func (a *App) CtrlOpenOperatorFolder(agentName string) error {
	d := filepath.Join(operatorRoot(), safeName(agentName))
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", d).Start()
	case "darwin":
		return exec.Command("open", d).Start()
	}
	return exec.Command("xdg-open", d).Start()
}

func safeName(n string) string {
	n = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, n)
	n = strings.TrimRight(n, ". ")
	if n == "" {
		n = "Agent"
	}
	return n
}

type sentRec struct {
	Size  int64 `json:"size"`
	MTime int64 `json:"mtime"`
}

func (a *App) watchOperatorFolders() {
	var mu sync.Mutex
	busy := map[string]bool{}
	seen := map[string]sentRec{} // last observed, to wait until a copy settles
	for {
		time.Sleep(2 * time.Second)
		c, err := a.getCtrl()
		if err != nil {
			return
		}
		for _, ag := range c.Agents() {
			if !ag.Online {
				continue
			}
			dir := filepath.Join(operatorRoot(), safeName(ag.Name))
			if _, err := os.Stat(dir); err != nil {
				continue
			}
			ledgerPath := filepath.Join(dir, ".waymark-sent.json")
			ledger := map[string]sentRec{}
			if b, err := os.ReadFile(ledgerPath); err == nil {
				json.Unmarshal(b, &ledger)
			}
			filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") || strings.HasSuffix(d.Name(), ".waymark-part") {
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return nil
				}
				r, _ := filepath.Rel(dir, p)
				rel := filepath.ToSlash(r)
				if _, err := files.Clean(rel); err != nil {
					return nil
				}
				now := sentRec{Size: info.Size(), MTime: info.ModTime().UnixNano()}
				key := ag.ID + "\x00" + rel
				mu.Lock()
				prev, stable := seen[key]
				seen[key] = now
				if ledger[rel] == now || busy[key] || !stable || prev != now {
					mu.Unlock()
					return nil
				}
				busy[key] = true
				mu.Unlock()
				go func(agentID, p, rel string, rec sentRec) {
					defer func() { mu.Lock(); delete(busy, key); mu.Unlock() }()
					if _, err := c.Upload(agentID, p, rel); err != nil {
						a.logf("auto-send %s: %v", rel, err)
						return
					}
					mu.Lock()
					l := map[string]sentRec{}
					if b, err := os.ReadFile(ledgerPath); err == nil {
						json.Unmarshal(b, &l)
					}
					l[rel] = rec
					b, _ := json.MarshalIndent(l, "", " ")
					os.WriteFile(ledgerPath, b, 0o644)
					mu.Unlock()
					a.emit("ctrl:refresh", agentID)
				}(ag.ID, p, rel, now)
				return nil
			})
		}
	}
}
