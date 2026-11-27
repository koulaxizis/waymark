package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/koulaxizis/waymark/app/internal/agent"
	"github.com/koulaxizis/waymark/app/internal/controller"
	"github.com/koulaxizis/waymark/app/internal/proto"
	"github.com/koulaxizis/waymark/app/internal/relay"
	"github.com/koulaxizis/waymark/app/internal/rtc"
)

// Config is saved next to the exe as waymark-remote.json.
type Config struct {
	Mode        string     `json:"mode"` // "", "agent", "controller"
	Name        string     `json:"name"` // device or operator name
	Role        proto.Role `json:"role"` // controller role
	Lang        string     `json:"lang"` // el, en
	Theme       string     `json:"theme"`
	Folder      string     `json:"folder"`      // Agent folder; "" = Waymark next to the exe
	UpdateCheck bool       `json:"updateCheck"` // look for a new stable version at start
	// UpdateAsked: the user has answered the first-start question about the
	// update check (0.1.2). Until then the app sends nothing to GitHub.
	UpdateAsked bool   `json:"updateAsked"`
	App         string `json:"app"` // preferred application: auto, powerpoint, viewer, ...
	// RelayURL and RelayKey: the relay a Controller offers its Agents for
	// when they share no network (0.1.1). Off when RelayURL is empty.
	RelayURL string `json:"relayUrl"`
	RelayKey string `json:"relayKey"`
}

// App is bound to the frontend: every exported method is callable from JS.
type App struct {
	ctx context.Context

	mu   sync.Mutex
	cfg  Config
	path string

	agent  *agent.Agent
	viewer *viewer
	invite *rtc.Invite
	ctrl   *controller.Controller

	prompt chan promptAnswer
}

type promptAnswer struct {
	ok   bool
	role proto.Role
}

func NewApp() *App {
	a := &App{path: filepath.Join(dataDir(), "waymark-remote.json")}
	a.cfg = Config{Lang: "el", Theme: "dark", App: "auto"}
	if b, err := os.ReadFile(a.path); err == nil {
		json.Unmarshal(b, &a.cfg)
	}
	if a.cfg.Name == "" {
		h, _ := os.Hostname()
		a.cfg.Name = h
	}
	return a
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	wr.OnFileDrop(ctx, a.onDrop)
	// The update check sends one request to GitHub (and with it the IP
	// address), so it runs only after the user said yes to it.
	if a.cfg.UpdateAsked && a.cfg.UpdateCheck {
		go a.checkUpdate()
	}
}

// AnswerUpdateQuestion stores the answer to the first-start question
// "Check for new versions?" and, on yes, checks once now.
func (a *App) AnswerUpdateQuestion(yes bool) error {
	a.mu.Lock()
	a.cfg.UpdateAsked, a.cfg.UpdateCheck = true, yes
	err := a.save()
	a.mu.Unlock()
	if yes {
		go a.checkUpdate()
	}
	return err
}

func (a *App) save() error {
	b, _ := json.MarshalIndent(a.cfg, "", "  ")
	// 0600: the file may hold the relay access key. Windows ignores the
	// mode; the Settings text tells the user to keep the folder private.
	if err := os.WriteFile(a.path, b, 0o600); err != nil {
		return err
	}
	return os.Chmod(a.path, 0o600) // also for a file an older version wrote as 0644
}

func (a *App) emit(name string, data any) {
	if a.ctx != nil {
		wr.EventsEmit(a.ctx, name, data)
	}
}

func (a *App) logf(format string, args ...any) {
	line := time.Now().Format("15:04:05") + "  " + fmt.Sprintf(format, args...)
	a.emit("log", line)
}

// Info is what the frontend needs at start.
type Info struct {
	Version string `json:"version"`
	OS      string `json:"os"`
	Config  Config `json:"config"`
	Running string `json:"running"` // mode in use this session
}

// GetInfo returns the version, the OS and the saved settings.
func (a *App) GetInfo() Info {
	a.mu.Lock()
	defer a.mu.Unlock()
	run := ""
	if a.agent != nil {
		run = "agent"
	} else if a.ctrl != nil {
		run = "controller"
	}
	return Info{Version: Version, OS: runtime.GOOS, Config: a.cfg, Running: run}
}

// SaveSettings stores language, theme, name, update check and app choice.
func (a *App) SaveSettings(c Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Lang, a.cfg.Theme, a.cfg.UpdateCheck = c.Lang, c.Theme, c.UpdateCheck
	a.cfg.UpdateAsked = true // a choice in the settings is an answer too
	if c.App != "" {
		a.cfg.App = c.App
	}
	if n := strings.TrimSpace(c.Name); n != "" {
		a.cfg.Name = n
	}
	a.cfg.RelayURL, a.cfg.RelayKey = strings.TrimSpace(c.RelayURL), strings.TrimSpace(c.RelayKey)
	if a.cfg.RelayURL != "" && relay.CleanURL(a.cfg.RelayURL) == "" {
		return errors.New("the relay address is not a web address")
	}
	if a.ctrl != nil {
		a.ctrl.SetRelay(a.cfg.RelayURL, a.cfg.RelayKey)
	}
	return a.save()
}

// Start runs the chosen mode for this session and remembers it.
func (a *App) Start(mode string, role proto.Role, name string) error {
	a.mu.Lock()
	if a.agent != nil || a.ctrl != nil {
		a.mu.Unlock()
		return errors.New("already running; restart the app to change mode")
	}
	if n := strings.TrimSpace(name); n != "" {
		a.cfg.Name = n
	}
	a.cfg.Mode = mode
	a.mu.Unlock()
	var err error
	switch mode {
	case "agent":
		err = a.startAgent()
	case "controller":
		if !role.Valid() {
			return errors.New("choose a role")
		}
		a.mu.Lock()
		a.cfg.Role = role
		a.mu.Unlock()
		err = a.startController()
	default:
		return errors.New("unknown mode")
	}
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.save()
}

// ResetMode forgets the saved mode so the next start asks again.
func (a *App) ResetMode() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Mode = ""
	return a.save()
}

// OpenURL opens a link in the system browser (release notes, downloads).
func (a *App) OpenURL(u string) {
	if strings.HasPrefix(u, "https://github.com/koulaxizis/waymark/") || strings.HasPrefix(u, "https://waymarked.site/") {
		wr.BrowserOpenURL(a.ctx, u)
	}
}

// serveFile serves the PDF of the built-in viewer at /viewer/file.
func (a *App) serveFile(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/viewer/file" {
		http.NotFound(w, r)
		return
	}
	a.mu.Lock()
	v := a.viewer
	a.mu.Unlock()
	if v == nil {
		http.NotFound(w, r)
		return
	}
	p := v.path()
	if p == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, p)
}

func (a *App) beforeClose() (prevent bool) {
	a.mu.Lock()
	ag, cfgFolder := a.agent, a.cfg.Folder
	lang := a.cfg.Lang
	a.mu.Unlock()
	if ag == nil || cfgFolder != "" {
		return false
	}
	entries, _ := ag.Store().List()
	if len(entries) == 0 {
		return false
	}
	title, msg := "Waymark", "Να σβηστούν τα αρχεία που πήρε το Agent από αυτόν τον υπολογιστή;"
	if lang == "en" {
		msg = "Delete the files the Agent received from this computer?"
	}
	ans, err := wr.MessageDialog(a.ctx, wr.MessageDialogOptions{Type: wr.QuestionDialog, Title: title, Message: msg})
	if err == nil && (ans == "Yes" || ans == "Ναι") {
		ag.Pres().Close()
		ag.Store().Clear()
	}
	return false
}

func (a *App) shutdown() {
	a.mu.Lock()
	ag, c := a.agent, a.ctrl
	a.mu.Unlock()
	if ag != nil {
		ag.Close()
		a.AgentCloseInvite()
	}
	if c != nil {
		c.Close()
	}
	keepAwake(false)
}
