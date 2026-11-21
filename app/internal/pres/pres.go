// Package pres opens and drives a presentation on the Agent.
//
// A Driver speaks to one application. With automation (PowerPoint COM, the
// built-in PDF viewer) every command works and the slide number n/N is known.
// With keystrokes (LibreOffice Impress, the system PDF viewer) only Next, Prev,
// Black and End are reliable, and the slide number is unknown.
package pres

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"

	"github.com/koulaxizis/waymark/app/internal/proto"
)

// Error carries a protocol error code.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Errf builds an Error.
func Errf(code, msg string) error { return &Error{Code: code, Msg: msg} }

// CodeOf returns the protocol code of err.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return proto.ErrIO
}

var errUnsupported = Errf(proto.ErrUnsupported, "this application cannot do that")

// Status is what a driver knows.
type Status struct {
	Slide  int    // 0 = unknown
	Slides int    // 0 = unknown
	State  string // proto.StatusOpen, StatusShowing, StatusBlack
}

// Driver drives one open presentation.
type Driver interface {
	Name() string
	Start(from int) error
	Next() error
	Prev() error
	Goto(n int) error
	Black(on bool) error
	End() error
	Close() error
	Status() Status
}

// Opener opens a file with one application, or says it cannot.
type Opener struct {
	Name string
	// Exts this application opens.
	Exts []string
	// Open returns a driver, or an error with APP_NOT_FOUND if the
	// application is missing on this laptop.
	Open func(path string) (Driver, error)
}

func (o Opener) handles(ext string) bool {
	for _, e := range o.Exts {
		if e == ext {
			return true
		}
	}
	return false
}

// Manager holds the one open presentation.
type Manager struct {
	mu      sync.Mutex
	openers []Opener
	cur     Driver
	file    string
}

// NewManager uses the openers in order for "auto".
func NewManager(openers ...Opener) *Manager { return &Manager{openers: openers} }

// Add appends an opener (the UI adds the built-in viewer once it runs).
func (m *Manager) Add(o Opener) {
	m.mu.Lock()
	m.openers = append(m.openers, o)
	m.mu.Unlock()
}

// Prepend puts an opener first.
func (m *Manager) Prepend(o Opener) {
	m.mu.Lock()
	m.openers = append([]Opener{o}, m.openers...)
	m.mu.Unlock()
}

// Apps lists the application names available for a file.
func (m *Manager) Apps(path string) []string {
	ext := strings.ToLower(filepath.Ext(path))
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, o := range m.openers {
		if o.handles(ext) {
			out = append(out, o.Name)
		}
	}
	return out
}

// Open closes what is open and opens path with app ("" or "auto" tries each
// application for the type in order).
func (m *Manager) Open(path, app string) error {
	ext := strings.ToLower(filepath.Ext(path))
	if !proto.OpenableExt[ext] {
		return Errf(proto.ErrFileType, "the Agent opens only .pptx .ppt .key .odp .pdf")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur != nil {
		m.cur.Close()
		m.cur, m.file = nil, ""
	}
	var last error = Errf(proto.ErrAppNotFound, "no application on this laptop opens "+ext)
	for _, o := range m.openers {
		if !o.handles(ext) || (app != "" && app != "auto" && app != o.Name) {
			continue
		}
		d, err := o.Open(path)
		if err == nil {
			m.cur, m.file = d, path
			return nil
		}
		last = err
		if app != "" && app != "auto" {
			break
		}
	}
	return last
}

func (m *Manager) do(f func(Driver) error) error {
	m.mu.Lock()
	d := m.cur
	m.mu.Unlock()
	if d == nil {
		return Errf(proto.ErrBadRequest, "no presentation is open")
	}
	return f(d)
}

func (m *Manager) Start(from int) error { return m.do(func(d Driver) error { return d.Start(from) }) }
func (m *Manager) Next() error          { return m.do(Driver.Next) }
func (m *Manager) Prev() error          { return m.do(Driver.Prev) }
func (m *Manager) Goto(n int) error {
	if n < 1 {
		return Errf(proto.ErrBadRequest, "slide numbers start at 1")
	}
	return m.do(func(d Driver) error { return d.Goto(n) })
}
func (m *Manager) Black(on bool) error { return m.do(func(d Driver) error { return d.Black(on) }) }
func (m *Manager) End() error          { return m.do(Driver.End) }

// Close closes the presentation (only one the Agent opened).
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return nil
	}
	err := m.cur.Close()
	m.cur, m.file = nil, ""
	return err
}

// Current returns the open file, the driver name and its status.
func (m *Manager) Current() (file, app string, st Status) {
	m.mu.Lock()
	d, f := m.cur, m.file
	m.mu.Unlock()
	if d == nil {
		return "", "", Status{State: proto.StatusIdle}
	}
	return f, d.Name(), d.Status()
}
