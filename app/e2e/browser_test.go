//go:build browser

// Package e2e runs the browser Controller (remote/ on the site) in headless
// Chromium against a real Agent over flow B. It needs Node and Playwright:
//
//	go test -tags browser ./e2e
package e2e

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/koulaxizis/waymark/app/internal/agent"
	"github.com/koulaxizis/waymark/app/internal/files"
	"github.com/koulaxizis/waymark/app/internal/pres"
	"github.com/koulaxizis/waymark/app/internal/proto"
	"github.com/koulaxizis/waymark/app/internal/rtc"
)

type fakeDriver struct {
	mu    sync.Mutex
	slide int
	state string
}

func (f *fakeDriver) Name() string { return "viewer" }
func (f *fakeDriver) Start(from int) error {
	f.mu.Lock()
	f.state, f.slide = proto.StatusShowing, max(from, 1)
	f.mu.Unlock()
	return nil
}
func (f *fakeDriver) Next() error {
	f.mu.Lock()
	f.slide = min(f.slide+1, 5)
	f.mu.Unlock()
	return nil
}
func (f *fakeDriver) Prev() error {
	f.mu.Lock()
	f.slide = max(f.slide-1, 1)
	f.mu.Unlock()
	return nil
}
func (f *fakeDriver) Goto(n int) error {
	f.mu.Lock()
	f.slide = n
	f.mu.Unlock()
	return nil
}
func (f *fakeDriver) Black(on bool) error { return nil }
func (f *fakeDriver) End() error          { return nil }
func (f *fakeDriver) Close() error        { return nil }
func (f *fakeDriver) Status() pres.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return pres.Status{Slide: f.slide, Slides: 5, State: f.state}
}

type ui struct {
	t   *testing.T
	mu  sync.Mutex
	sas string
}

func (u *ui) Prompt(sas, name string, role proto.Role) (proto.Role, bool) {
	u.mu.Lock()
	u.sas = sas
	u.mu.Unlock()
	u.t.Logf("prompt: %s as %s, SAS %s", name, role, sas)
	return role, true
}
func (u *ui) State(proto.State)              {}
func (u *ui) Event(proto.Event)              {}
func (u *ui) Log(format string, args ...any) { u.t.Logf("agent: "+format, args...) }

// resumer adapts rtc.Resume to agent.Resumer, as the app does.
type resumer struct{ r *rtc.Resume }

func (x resumer) Wait(stop <-chan struct{}) (agent.Link, error) {
	l, err := x.r.Wait(stop)
	if err != nil {
		return nil, err
	}
	return l, nil
}
func (x resumer) TryCode(c string) bool { return x.r.TryCode(c) }
func (x resumer) Port() int             { return x.r.Port() }

func TestBrowserController(t *testing.T) {
	root, _ := filepath.Abs("../../remote")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go http.Serve(ln, http.FileServer(http.Dir(root)))
	rtc.PageURL = "http://" + ln.Addr().String() + "/"

	dir := t.TempDir()
	store, err := files.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "talk.pdf"), []byte("%PDF-1.4 fake"), 0o644)
	big := make([]byte, 300<<10+123)
	rand.Read(big)
	os.WriteFile(filepath.Join(dir, "big.bin"), big, 0o644)
	pm := pres.NewManager(pres.Opener{Name: "viewer", Exts: []string{".pdf"}, Open: func(string) (pres.Driver, error) {
		return &fakeDriver{state: proto.StatusOpen, slide: 1}, nil
	}})
	u := &ui{t: t}
	ag := agent.New("Test laptop", "0.1.0-test", store, pm, u)
	defer ag.Close()

	inv, err := rtc.NewInvite(ag.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer inv.Close()
	out := t.TempDir()
	codeFile := filepath.Join(out, "code.txt")
	upload := filepath.Join(out, "notes.txt")
	upData := bytes.Repeat([]byte("Waymark upload test line.\n"), 9000) // ~230 KB, several chunks
	os.WriteFile(upload, upData, 0o644)

	cmd := exec.Command("node", "browser.mjs", inv.URL, codeFile, upload, out)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Skip("node not available:", err)
	}
	var code string
	for i := 0; i < 300 && code == ""; i++ {
		b, _ := os.ReadFile(codeFile)
		code = strings.TrimSpace(string(b))
		time.Sleep(100 * time.Millisecond)
	}
	if code == "" {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("page showed no return code\n%s\n%s", stdout.String(), stderr.String())
	}
	t.Logf("return code %s", code)
	acc, err := inv.Accept(code, u.Prompt)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("accept: %v\n%s\n%s", err, stdout.String(), stderr.String())
	}
	ag.Attach(acc.ID, acc.Name, acc.Role, acc.Link, resumer{acc.Resume})

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-time.After(150 * time.Second):
		cmd.Process.Kill()
		err = <-done
	}
	t.Logf("page:\n%s", stdout.String())
	if err != nil {
		t.Fatalf("page script: %v\n%s", err, stderr.String())
	}
	var res struct {
		SAS, Slide, Resumed, DownloadSHA string
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil {
		t.Fatalf("result: %v", err)
	}
	if res.SAS != u.sas {
		t.Errorf("SAS: page %q, agent %q", res.SAS, u.sas)
	}
	if res.Slide != "2 / 5" {
		t.Errorf("slide after Next: %q", res.Slide)
	}
	if res.Resumed != "3 / 5" {
		t.Errorf("slide after a reload and Next: %q (the page did not come back)", res.Resumed)
	}
	got, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
	if err != nil || !bytes.Equal(got, upData) {
		t.Errorf("upload did not arrive intact (%v, %d bytes)", err, len(got))
	}
	dl, err := os.ReadFile(filepath.Join(out, "big.bin"))
	if err != nil || !bytes.Equal(dl, big) {
		t.Errorf("download did not arrive intact (%v, %d bytes)", err, len(dl))
	}
}
