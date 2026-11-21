package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/koulaxizis/waymark/app/internal/agent"
	"github.com/koulaxizis/waymark/app/internal/files"
	"github.com/koulaxizis/waymark/app/internal/pres"
	"github.com/koulaxizis/waymark/app/internal/proto"
)

func (a *App) agentFolder() string {
	if a.cfg.Folder != "" {
		return a.cfg.Folder
	}
	return filepath.Join(exeDir(), "Waymark")
}

func (a *App) startAgent() error {
	a.mu.Lock()
	folder, name := a.agentFolder(), a.cfg.Name
	a.mu.Unlock()
	store, err := files.New(folder)
	if err != nil {
		// Next to the exe may be read-only (e.g. a USB stick with write lock).
		home, _ := os.UserHomeDir()
		store, err = files.New(filepath.Join(home, "Waymark"))
		if err != nil {
			return err
		}
	}
	v := &viewer{app: a, state: proto.StatusIdle}
	openers := []pres.Opener{v.opener()}
	if runtime.GOOS == "windows" {
		openers = append(openers, pres.PowerPoint())
	}
	openers = append(openers,
		pres.Impress(filepath.Join(store.Root(), ".waymark", "libreoffice")),
		pres.SystemViewer())
	pm := pres.NewManager(openers...)
	ag := agent.New(name, Version, store, pm, &agentUI{app: a})
	a.mu.Lock()
	a.agent, a.viewer = ag, v
	a.prompt = make(chan promptAnswer, 1)
	a.mu.Unlock()
	a.logf("Agent ready, folder %s", store.Root())
	return nil
}

// agentUI forwards Agent callbacks to the window.
type agentUI struct{ app *App }

func (u *agentUI) Prompt(sas, peer string, role proto.Role) (proto.Role, bool) {
	a := u.app
	// Drop a stale answer.
	select {
	case <-a.prompt:
	default:
	}
	wr.WindowUnminimise(a.ctx)
	wr.WindowShow(a.ctx)
	a.emit("agent:prompt", map[string]any{"sas": sas, "name": peer, "role": role})
	select {
	case ans := <-a.prompt:
		a.emit("agent:prompt-done", nil)
		return ans.role, ans.ok
	case <-time.After(2 * time.Minute):
		a.emit("agent:prompt-done", nil)
		return "", false
	}
}

func (u *agentUI) State(s proto.State) {
	keepAwake(s.Status == proto.StatusShowing || s.Status == proto.StatusBlack)
	u.app.emit("agent:state", s)
}

func (u *agentUI) Event(e proto.Event) {
	if e.Kind == "pres.showing" {
		u.app.mu.Lock()
		v := u.app.viewer
		u.app.mu.Unlock()
		// Keep the Agent window off the projector while another app shows.
		if v == nil || !v.active() {
			wr.WindowMinimise(u.app.ctx)
		}
	}
	u.app.emit("agent:event", e)
}

func (u *agentUI) Log(format string, args ...any) { u.app.logf(format, args...) }

func (a *App) getAgent() (*agent.Agent, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.agent == nil {
		return nil, errors.New("the Agent is not running")
	}
	return a.agent, nil
}

// AgentConnect pairs with a Controller from the code the presenter typed.
// It returns when the link is up or pairing failed.
func (a *App) AgentConnect(code string) error {
	ag, err := a.getAgent()
	if err != nil {
		return err
	}
	return ag.Connect(code)
}

// AgentAnswer is the presenter's answer to a connection request.
func (a *App) AgentAnswer(accept bool, role proto.Role) {
	select {
	case a.prompt <- promptAnswer{ok: accept, role: role}:
	default:
	}
}

// AgentDisconnectAll closes every connection.
func (a *App) AgentDisconnectAll() {
	if ag, err := a.getAgent(); err == nil {
		ag.DisconnectAll()
	}
}

// AgentDisconnect closes one connection.
func (a *App) AgentDisconnect(id string) {
	if ag, err := a.getAgent(); err == nil {
		ag.Disconnect(id)
	}
}

// AgentState returns the current state.
func (a *App) AgentState() (proto.State, error) {
	ag, err := a.getAgent()
	if err != nil {
		return proto.State{}, err
	}
	return ag.State(), nil
}

// AgentFiles lists the Agent folder.
func (a *App) AgentFiles() ([]proto.Entry, error) {
	ag, err := a.getAgent()
	if err != nil {
		return nil, err
	}
	return ag.Store().List()
}

// AgentChooseFolder lets the presenter pick the folder. Only the Agent can
// change it, never a Controller. It applies at the next start.
func (a *App) AgentChooseFolder() (string, error) {
	d, err := wr.OpenDirectoryDialog(a.ctx, wr.OpenDialogOptions{Title: "Waymark"})
	if err != nil || d == "" {
		return "", err
	}
	a.mu.Lock()
	a.cfg.Folder = d
	err = a.save()
	a.mu.Unlock()
	return d, err
}

// AgentOpenFolder shows the folder in the file manager.
func (a *App) AgentOpenFolder() {
	ag, err := a.getAgent()
	if err != nil {
		return
	}
	root := ag.Store().Root()
	switch runtime.GOOS {
	case "windows":
		exec.Command("explorer", root).Start()
	case "darwin":
		exec.Command("open", root).Start()
	default:
		exec.Command("xdg-open", root).Start()
	}
}

// viewer is the built-in PDF viewer: pdf.js in this window, full screen on
// the projector. It knows the slide number and can jump and black out.
type viewer struct {
	app *App

	mu     sync.Mutex
	file   string
	slide  int
	slides int
	state  string
	shown  bool
}

func (v *viewer) opener() pres.Opener {
	return pres.Opener{Name: "viewer", Exts: []string{".pdf"}, Open: v.open}
}

func (v *viewer) path() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.file
}

func (v *viewer) active() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.file != ""
}

func (v *viewer) open(path string) (pres.Driver, error) {
	v.mu.Lock()
	v.file, v.slide, v.slides, v.state, v.shown = path, 1, 0, proto.StatusOpen, false
	v.mu.Unlock()
	v.app.emit("viewer:open", map[string]any{"name": filepath.Base(path), "t": time.Now().UnixNano()})
	return v, nil
}

func (v *viewer) Name() string { return "viewer" }

func (v *viewer) Start(from int) error {
	v.mu.Lock()
	if from < 1 {
		from = v.slide
		if !v.shown || from < 1 {
			from = 1
		}
	}
	v.shown = true
	v.state = proto.StatusShowing
	v.mu.Unlock()
	moveToProjector("Waymark Remote")
	wr.WindowShow(v.app.ctx)
	wr.WindowUnminimise(v.app.ctx)
	wr.WindowFullscreen(v.app.ctx)
	v.app.emit("viewer:cmd", map[string]any{"cmd": "start", "slide": from})
	return nil
}

func (v *viewer) cmd(c string, arg any) error {
	v.mu.Lock()
	shown := v.shown
	v.mu.Unlock()
	if !shown {
		return pres.Errf(proto.ErrBadRequest, "the slide show is not running; press Start")
	}
	v.app.emit("viewer:cmd", map[string]any{"cmd": c, "slide": arg})
	return nil
}

func (v *viewer) Next() error { return v.cmd("next", nil) }
func (v *viewer) Prev() error { return v.cmd("prev", nil) }
func (v *viewer) Goto(n int) error {
	v.mu.Lock()
	total := v.slides
	v.mu.Unlock()
	if total > 0 && n > total {
		return pres.Errf(proto.ErrBadRequest, "the presentation has fewer slides")
	}
	return v.cmd("goto", n)
}

func (v *viewer) Black(on bool) error {
	if err := v.cmd("black", on); err != nil {
		return err
	}
	v.mu.Lock()
	if on {
		v.state = proto.StatusBlack
	} else {
		v.state = proto.StatusShowing
	}
	v.mu.Unlock()
	return nil
}

func (v *viewer) End() error {
	v.mu.Lock()
	v.shown = false
	v.state = proto.StatusOpen
	v.mu.Unlock()
	wr.WindowUnfullscreen(v.app.ctx)
	v.app.emit("viewer:cmd", map[string]any{"cmd": "end"})
	return nil
}

func (v *viewer) Close() error {
	v.mu.Lock()
	wasShown := v.shown
	v.file, v.shown, v.state, v.slide, v.slides = "", false, proto.StatusIdle, 0, 0
	v.mu.Unlock()
	if wasShown {
		wr.WindowUnfullscreen(v.app.ctx)
	}
	v.app.emit("viewer:cmd", map[string]any{"cmd": "close"})
	return nil
}

func (v *viewer) Status() pres.Status {
	v.mu.Lock()
	defer v.mu.Unlock()
	return pres.Status{Slide: v.slide, Slides: v.slides, State: v.state}
}

// ViewerReport is called by the page after each change in the viewer.
func (a *App) ViewerReport(slide, slides int) {
	a.mu.Lock()
	v, ag := a.viewer, a.agent
	a.mu.Unlock()
	if v == nil {
		return
	}
	v.mu.Lock()
	v.slide, v.slides = slide, slides
	v.mu.Unlock()
	if ag != nil {
		ag.Changed() // push n/N to the Controllers now
	}
}

// ViewerLocalEnd is the presenter pressing Esc in the viewer.
func (a *App) ViewerLocalEnd() {
	a.mu.Lock()
	v := a.viewer
	a.mu.Unlock()
	if v != nil {
		v.End()
	}
}
