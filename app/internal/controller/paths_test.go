package controller_test

import (
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/koulaxizis/waymark/app/internal/agent"
	"github.com/koulaxizis/waymark/app/internal/pair"
	"github.com/koulaxizis/waymark/app/internal/proto"
	"github.com/koulaxizis/waymark/app/internal/relay"
)

// path is a TCP forwarder that stands for one network (a cable, Wi-Fi). cut
// makes it a black hole, as when a cable is pulled: no new connections, and
// the open ones go silent without closing.
type path struct {
	t      *testing.T
	ip     string
	port   int
	target string
	mu     sync.Mutex
	ln     net.Listener
	conns  []net.Conn
	frozen bool
}

func newPath(t *testing.T, ip string, port int, target string) *path {
	p := &path{t: t, ip: ip, port: port, target: target}
	p.up()
	t.Cleanup(p.cut)
	return p
}

func (p *path) up() {
	ln, err := net.Listen("tcp4", net.JoinHostPort(p.ip, strconv.Itoa(p.port)))
	if err != nil {
		p.t.Skip("loopback alias not available:", err)
	}
	p.mu.Lock()
	p.ln, p.frozen = ln, false
	p.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			d, err := net.Dial("tcp4", p.target)
			if err != nil {
				c.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, c, d)
			p.mu.Unlock()
			go p.pipe(c, d)
			go p.pipe(d, c)
		}
	}()
}

func (p *path) pipe(dst, src net.Conn) {
	defer dst.Close() // pass the close on, as a real network does
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if err != nil {
			return
		}
		p.mu.Lock()
		frozen := p.frozen
		p.mu.Unlock()
		if frozen {
			io.Copy(io.Discard, src) // swallow everything from now on
			select {}                // and never close: a pulled cable sends no FIN
		}
		if _, err := dst.Write(buf[:n]); err != nil {
			return
		}
	}
}

func (p *path) cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.frozen = true
	if p.ln != nil {
		p.ln.Close()
		p.ln = nil
	}
}

var upgradeOnce sync.Once

// fastUpgrade shortens the upgrade check once: Agents of an earlier test may
// still read it.
func fastUpgrade() { upgradeOnce.Do(func() { agent.UpgradeEvery = 300 * time.Millisecond }) }

func agentPath(a *agent.Agent) string {
	for _, pe := range a.State().Peers {
		return pe.Path
	}
	return ""
}

func waitFor(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestPathFailover: the Agent moves to the cable, falls over to Wi-Fi when
// the cable goes silent (a slide command in flight runs exactly once), and
// moves back to the cable when it returns (Proposal 13).
func TestPathFailover(t *testing.T) {
	fastUpgrade()
	l, err := net.Listen("tcp4", "127.0.0.2:0")
	if err != nil {
		t.Skip("no 127.0.0.2:", err)
	}
	pport := l.Addr().(*net.TCPAddr).Port
	l.Close()
	pair.ListAddrs = func(int) proto.AddrsData {
		return proto.AddrsData{Port: pport, Addrs: []proto.Addr{{IP: "127.0.0.2", Kind: "wired"}, {IP: "127.0.0.3", Kind: "wifi"}}}
	}
	a, c, fd, id, _ := setup(t, proto.RoleOperator, "")
	target := "127.0.0.1:" + strconv.Itoa(portOf(t, c))
	wired := newPath(t, "127.0.0.2", pport, target)
	newPath(t, "127.0.0.3", pport, target)

	waitFor(t, "the move to the cable", 5*time.Second, func() bool { return agentPath(a) == "wired" })
	time.Sleep(1200 * time.Millisecond) // the old link closes after 1 s

	if err := c.Take(id); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(a.Store().Root(), "talk.pdf"), []byte("%PDF-1.4"), 0o644)
	if _, err := c.Call(id, proto.CmdPresOpen, proto.OpenArgs{Path: "talk.pdf"}); err != nil {
		t.Fatalf("open: %v", err)
	}
	fd.mu.Lock()
	fd.slide, fd.state = 3, proto.StatusShowing
	fd.mu.Unlock()

	// Pull the cable, then press Next before anyone notices.
	t.Logf("before cut: agent %+v ctrl %+v", a.State().Peers, c.Agents()[0].Path)
	wired.cut()
	t0 := time.Now()
	if _, err := c.Call(id, proto.CmdPresNext, nil); err != nil {
		t.Fatalf("next across the failover: %v", err)
	}
	t.Logf("next answered after %v", time.Since(t0).Round(time.Millisecond))
	waitFor(t, "Wi-Fi", 5*time.Second, func() bool { return agentPath(a) == "wifi" })
	fd.mu.Lock()
	slide := fd.slide
	fd.mu.Unlock()
	if slide != 4 {
		t.Fatalf("slide %d after one Next, want 4", slide)
	}

	// The cable comes back.
	wired.up()
	waitFor(t, "the move back to the cable", 5*time.Second, func() bool { return agentPath(a) == "wired" })
	if _, err := c.Call(id, proto.CmdPresNext, nil); err != nil {
		t.Fatal(err)
	}
	fd.mu.Lock()
	defer fd.mu.Unlock()
	if fd.slide != 5 {
		t.Fatalf("slide %d, want 5", fd.slide)
	}
}

// TestRelayFailover: with no network left the link goes over the relay, a
// slide command runs once, and the link moves back when the cable returns
// (Proposal 13, 0.1.1). The relay needs its access key.
func TestRelayFailover(t *testing.T) {
	fastUpgrade()
	l, err := net.Listen("tcp4", "127.0.0.2:0")
	if err != nil {
		t.Skip("no 127.0.0.2:", err)
	}
	pport := l.Addr().(*net.TCPAddr).Port
	l.Close()
	pair.ListAddrs = func(int) proto.AddrsData {
		return proto.AddrsData{Port: pport, Addrs: []proto.Addr{{IP: "127.0.0.2", Kind: "wired"}}}
	}
	// WM_RELAY_URL and WM_RELAY_KEY run it through a Worker (wrangler dev).
	ru, rk := os.Getenv("WM_RELAY_URL"), os.Getenv("WM_RELAY_KEY")
	if ru == "" {
		srv := httptest.NewServer(relay.Handler("sesame"))
		t.Cleanup(srv.Close)
		ru, rk = srv.URL, "sesame"
	}
	a, c, fd, id, _ := setup(t, proto.RoleOperator, "")
	target := "127.0.0.1:" + strconv.Itoa(portOf(t, c))
	wired := newPath(t, "127.0.0.2", pport, target)
	c.SetRelay(ru, rk)

	waitFor(t, "the move to the cable", 5*time.Second, func() bool { return agentPath(a) == "wired" })
	time.Sleep(1200 * time.Millisecond)
	if err := c.Take(id); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(a.Store().Root(), "talk.pdf"), []byte("%PDF-1.4"), 0o644)
	if _, err := c.Call(id, proto.CmdPresOpen, proto.OpenArgs{Path: "talk.pdf"}); err != nil {
		t.Fatalf("open: %v", err)
	}
	fd.mu.Lock()
	fd.slide, fd.state = 3, proto.StatusShowing
	fd.mu.Unlock()

	wired.cut()
	t0 := time.Now()
	if _, err := c.Call(id, proto.CmdPresNext, nil); err != nil {
		t.Fatalf("next across the failover: %v", err)
	}
	t.Logf("next answered over %s after %v", agentPath(a), time.Since(t0).Round(time.Millisecond))
	waitFor(t, "the relay", 5*time.Second, func() bool { return agentPath(a) == "relay" })
	if p := c.Agents()[0].Path; p != "relay" {
		t.Errorf("controller shows path %q, want relay", p)
	}
	fd.mu.Lock()
	slide := fd.slide
	fd.mu.Unlock()
	if slide != 4 {
		t.Fatalf("slide %d after one Next, want 4", slide)
	}

	wired.up()
	waitFor(t, "the move back to the cable", 5*time.Second, func() bool { return agentPath(a) == "wired" })
	if _, err := c.Call(id, proto.CmdPresNext, nil); err != nil {
		t.Fatal(err)
	}
	fd.mu.Lock()
	defer fd.mu.Unlock()
	if fd.slide != 5 {
		t.Fatalf("slide %d, want 5", fd.slide)
	}
}
