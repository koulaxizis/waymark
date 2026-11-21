package controller_test

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/koulaxizis/waymark/app/internal/agent"
	"github.com/koulaxizis/waymark/app/internal/code"
	"github.com/koulaxizis/waymark/app/internal/controller"
	"github.com/koulaxizis/waymark/app/internal/files"
	"github.com/koulaxizis/waymark/app/internal/pair"
	"github.com/koulaxizis/waymark/app/internal/pres"
	"github.com/koulaxizis/waymark/app/internal/proto"
)

// fakeDriver records commands and keeps a slide counter.
type fakeDriver struct {
	mu    sync.Mutex
	slide int
	n     int
	state string
}

func (f *fakeDriver) Name() string { return "fake" }
func (f *fakeDriver) Start(from int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state, f.slide = proto.StatusShowing, max(from, 1)
	return nil
}
func (f *fakeDriver) Next() error {
	f.mu.Lock()
	f.slide = min(f.slide+1, f.n)
	f.mu.Unlock()
	return nil
}
func (f *fakeDriver) Prev() error {
	f.mu.Lock()
	f.slide = max(f.slide-1, 1)
	f.mu.Unlock()
	return nil
}
func (f *fakeDriver) Goto(n int) error { f.mu.Lock(); f.slide = n; f.mu.Unlock(); return nil }
func (f *fakeDriver) Black(on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if on {
		f.state = proto.StatusBlack
	} else {
		f.state = proto.StatusShowing
	}
	return nil
}
func (f *fakeDriver) End() error   { f.mu.Lock(); f.state = proto.StatusOpen; f.mu.Unlock(); return nil }
func (f *fakeDriver) Close() error { return nil }
func (f *fakeDriver) Status() pres.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return pres.Status{Slide: f.slide, Slides: f.n, State: f.state}
}

type agentUI struct {
	grant proto.Role
	mu    sync.Mutex
	sas   string
	evs   []proto.Event
}

func (u *agentUI) Prompt(sas, name string, role proto.Role) (proto.Role, bool) {
	u.mu.Lock()
	u.sas = sas
	u.mu.Unlock()
	if u.grant != "" {
		return u.grant, true
	}
	return role, true
}
func (u *agentUI) State(proto.State) {}
func (u *agentUI) Event(e proto.Event) {
	u.mu.Lock()
	u.evs = append(u.evs, e)
	u.mu.Unlock()
}
func (u *agentUI) Log(f string, a ...any) {
	if os.Getenv("WM_LOG") != "" {
		fmt.Printf("agent: "+f+"\n", a...)
	}
}

type ctrlUI struct {
	mu  sync.Mutex
	sas string
}

func (u *ctrlUI) Codes([]pair.Code) {}
func (u *ctrlUI) SAS(sas, _ string) {
	u.mu.Lock()
	u.sas = sas
	u.mu.Unlock()
}
func (u *ctrlUI) Agents([]controller.AgentInfo) {}
func (u *ctrlUI) Event(string, proto.Event)     {}
func (u *ctrlUI) Progress(controller.Progress)  {}
func (u *ctrlUI) Log(f string, a ...any) {
	if os.Getenv("WM_LOG") != "" {
		fmt.Printf("ctrl: "+f+"\n", a...)
	}
}

func loopCode(t *testing.T, c *controller.Controller, port int) string {
	t.Helper()
	// The codes carry LAN addresses; for the test, rebuild the long code for
	// 127.0.0.1 with the same secret by parsing one of them.
	cs := c.Codes()
	if len(cs) == 0 {
		t.Skip("no network interface")
	}
	p, err := code.ParsePairCode(cs[0].Long)
	if err != nil {
		t.Fatal(err)
	}
	return code.PairCode{IP: net.IPv4(127, 0, 0, 1), Port: port, Secret: p.Secret}.Long()
}

func setup(t *testing.T, role proto.Role, grant proto.Role) (*agent.Agent, *controller.Controller, *fakeDriver, string, *agentUI) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	store, err := files.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fd := &fakeDriver{n: 12, state: proto.StatusOpen}
	pm := pres.NewManager(pres.Opener{Name: "fake", Exts: []string{".pdf", ".pptx"}, Open: func(string) (pres.Driver, error) { return fd, nil }})
	aui := &agentUI{grant: grant}
	a := agent.New("Αίθουσα Α", "test", store, pm, aui)
	cui := &ctrlUI{}
	c, err := controller.New("Χειριστής", role, cui)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); c.Close() })
	port := portOf(t, c)
	if err := a.Connect(loopCode(t, c, port)); err != nil {
		t.Fatal(err)
	}
	var id string
	deadline := time.Now().Add(3 * time.Second)
	for id == "" && time.Now().Before(deadline) {
		for _, ai := range c.Agents() {
			if ai.Online {
				id = ai.ID
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("agent never showed up")
	}
	cui.mu.Lock()
	aui.mu.Lock()
	if cui.sas == "" || cui.sas != aui.sas {
		t.Fatalf("SAS differ: %q %q", cui.sas, aui.sas)
	}
	aui.mu.Unlock()
	cui.mu.Unlock()
	return a, c, fd, id, aui
}

func portOf(t *testing.T, c *controller.Controller) int {
	cs := c.Codes()
	if len(cs) == 0 {
		t.Skip("no network interface")
	}
	p, _ := code.ParsePairCode(cs[0].Long)
	return p.Port
}

func codeOf(err error) string {
	var ce *controller.CallError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

func TestOperatorFlow(t *testing.T) {
	a, c, fd, id, aui := setup(t, proto.RoleOperator, "")
	// Upload a presentation, 200 KB, several chunks.
	src := filepath.Join(t.TempDir(), "Ομιλία.pptx")
	body := make([]byte, 200000)
	rand.Read(body)
	os.WriteFile(src, body, 0o644)
	final, err := c.Upload(id, src, "Συνεδρία 1/Ομιλία.pptx")
	if err != nil || final != "Συνεδρία 1/Ομιλία.pptx" {
		t.Fatalf("upload %q %v", final, err)
	}
	got, _ := os.ReadFile(filepath.Join(a.Store().Root(), "Συνεδρία 1", "Ομιλία.pptx"))
	if !bytes.Equal(got, body) {
		t.Fatal("uploaded content differs")
	}
	// Presentation commands need the key.
	if _, err := c.Call(id, proto.CmdPresOpen, proto.OpenArgs{Path: final}); codeOf(err) != proto.ErrNotHolder {
		t.Fatalf("open without key: %v", err)
	}
	if err := c.Take(id); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		cmd  string
		args any
	}{
		{proto.CmdPresOpen, proto.OpenArgs{Path: final}},
		{proto.CmdPresStart, nil},
		{proto.CmdPresNext, nil},
		{proto.CmdPresNext, nil},
		{proto.CmdPresPrev, nil},
		{proto.CmdPresGoto, proto.GotoArgs{Slide: 7}},
		{proto.CmdPresBlack, proto.BlackArgs{On: true}},
	} {
		if _, err := c.Call(id, step.cmd, step.args); err != nil {
			t.Fatalf("%s: %v", step.cmd, err)
		}
	}
	raw, err := c.Call(id, proto.CmdStateGet, nil)
	if err != nil {
		t.Fatal(err)
	}
	var st proto.State
	json.Unmarshal(raw, &st)
	if st.Slide != 7 || st.Slides != 12 || st.Status != proto.StatusBlack || st.File != final || st.Holder == nil {
		t.Fatalf("state %+v", st)
	}
	_ = fd
	// The open file cannot be deleted.
	if _, err := c.Call(id, proto.CmdFileDelete, proto.PathArgs{Path: final}); codeOf(err) != proto.ErrInUse {
		t.Fatalf("delete in use: %v", err)
	}
	// Download it back and compare.
	dst := filepath.Join(t.TempDir(), "back.pptx")
	if err := c.Download(id, final, dst); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(dst)
	if !bytes.Equal(back, body) {
		t.Fatal("downloaded content differs")
	}
	// Listing shows the open file.
	raw, _ = c.Call(id, proto.CmdFileList, nil)
	var l []proto.Entry
	json.Unmarshal(raw, &l)
	if len(l) != 2 || !l[1].Open {
		t.Fatalf("list %+v", l)
	}
	aui.mu.Lock()
	if len(aui.evs) == 0 || aui.evs[0].Kind != "file.new" {
		t.Fatalf("events %+v", aui.evs)
	}
	aui.mu.Unlock()
}

func TestSecretariatLimits(t *testing.T) {
	_, c, _, id, _ := setup(t, proto.RoleSecretariat, "")
	if err := c.Take(id); codeOf(err) != proto.ErrNotAllowed {
		t.Fatalf("secretariat took control: %v", err)
	}
	if _, err := c.Call(id, proto.CmdPresNext, nil); codeOf(err) != proto.ErrNotAllowed {
		t.Fatalf("secretariat drove slides: %v", err)
	}
	if _, err := c.Call(id, proto.CmdFileMkdir, proto.PathArgs{Path: "Αίθουσα Α/Δευτέρα"}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentLowersRole(t *testing.T) {
	_, c, _, id, _ := setup(t, proto.RoleSupervisor, proto.RoleSecretariat)
	for _, ai := range c.Agents() {
		if ai.ID == id && ai.Role != proto.RoleSecretariat {
			t.Fatalf("role %s", ai.Role)
		}
	}
	if err := c.Take(id); codeOf(err) != proto.ErrNotAllowed {
		t.Fatalf("lowered role took control: %v", err)
	}
}

func TestDisconnectAll(t *testing.T) {
	a, c, _, id, _ := setup(t, proto.RoleOperator, "")
	a.DisconnectAll()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(c.Agents()) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("controller still lists %v", c.Agents())
	_ = id
}
