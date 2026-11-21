// Package controller is the Waymark Remote Controller: it listens, shows the
// pairing code, keeps one link per Agent, and gives the UI calls for commands,
// uploads and downloads.
package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/koulaxizis/waymark/app/internal/netif"
	"github.com/koulaxizis/waymark/app/internal/pair"
	"github.com/koulaxizis/waymark/app/internal/proto"
	"github.com/koulaxizis/waymark/app/internal/secure"
)

// Timings (Proposal 13): ping every 1 s, a link is lost after 3 s of
// silence. A slide command that was sent but not answered waits up to
// HoldFor for the Agent to come back (over any path) and is sent again; the
// Agent's sequence numbers make sure it runs once.
var (
	PingEvery   = time.Second
	LostAfter   = 3 * time.Second
	CallTimeout = 15 * time.Second
	HoldFor     = 10 * time.Second
)

// UI receives what the Controller window shows.
type UI interface {
	Codes([]pair.Code)
	SAS(sas, agentName string)
	Agents([]AgentInfo)
	Event(agentID string, ev proto.Event)
	Progress(p Progress)
	Log(format string, args ...any)
}

// AgentInfo is one Agent as the UI lists it.
type AgentInfo struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Role      proto.Role   `json:"role"` // granted by the Agent
	Online    bool         `json:"online"`
	Path      string       `json:"path,omitempty"` // wired, wifi, cell, other
	LatencyMs int64        `json:"latencyMs"`
	State     *proto.State `json:"state,omitempty"`
	Holder    bool         `json:"holder"` // this Controller holds the key
}

// Progress of one transfer.
type Progress struct {
	Agent string `json:"agent"`
	Path  string `json:"path"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Up    bool   `json:"up"`
	State string `json:"state"` // running, ok, error
	Err   string `json:"err,omitempty"`
}

// Controller is one running Controller.
type Controller struct {
	l  *pair.Listener
	ui UI

	mu     sync.Mutex
	agents map[string]*link
	idn    atomic.Uint64
	addrs  string // the last address list sent, as JSON
}

type link struct {
	id, name string
	role     proto.Role
	conn     *secure.Conn
	path     string
	state    *proto.State
	latency  time.Duration
	seq      uint64
	pending  map[string]*pend
	streams  map[string]chan proto.Msg
	mu       sync.Mutex
}

// pend is a command waiting for its answer. Slide and control commands
// (Seq > 0) survive a lost link and are sent again on the next one.
type pend struct {
	ch     chan proto.Msg
	msg    proto.Msg
	resent bool
	lostAt time.Time
}

// keepsOverLoss tells which pending commands wait for the next link.
func (p *pend) keepsOverLoss() bool { return p.msg.Seq > 0 }

// dropPending fails the commands that cannot wait, or all of them, and
// remembers when the rest started waiting. Call with a.mu held.
func (a *link) dropPending(all bool) {
	now := time.Now()
	for id, p := range a.pending {
		if all || !p.keepsOverLoss() {
			close(p.ch)
			delete(a.pending, id)
		} else if p.lostAt.IsZero() {
			p.lostAt = now
		}
	}
	for id, ch := range a.streams {
		close(ch)
		delete(a.streams, id)
	}
}

// resend sends the waiting commands on a new link. Call with a.mu held.
func (a *link) resend(conn *secure.Conn) {
	for _, p := range a.pending {
		if p.keepsOverLoss() {
			p.resent, p.lostAt = true, time.Time{}
			go conn.Send(p.msg)
		}
	}
}

// New listens and starts accepting Agents.
func New(name string, role proto.Role, ui UI) (*Controller, error) {
	l, err := pair.Listen(name, role)
	if err != nil {
		return nil, err
	}
	c := &Controller{l: l, ui: ui, agents: map[string]*link{}}
	l.OnSAS = ui.SAS
	l.OnCode = func() { ui.Codes(l.Codes()) }
	l.OnRelay = func(err error) {
		if err != nil {
			ui.Log("relay: %v", err)
		} else {
			ui.Log("relay ready")
		}
	}
	go c.accept()
	go c.pinger()
	return c, nil
}

// Codes returns the current pairing codes.
func (c *Controller) Codes() []pair.Code { return c.l.Codes() }

// SetRelay sets the relay the Agents fall back to over the internet when
// no network path answers; an empty url turns it off (Proposal 13, 0.1.1).
func (c *Controller) SetRelay(url, key string) { c.l.SetRelay(url, key) }

// NewCode replaces the code.
func (c *Controller) NewCode() { c.l.NewCode() }

// Close stops everything.
func (c *Controller) Close() {
	c.l.Close()
	c.mu.Lock()
	for _, a := range c.agents {
		if a.conn != nil {
			a.conn.Send(proto.Msg{V: proto.Version, T: proto.TBye})
			a.conn.Close()
		}
	}
	c.mu.Unlock()
}

func (c *Controller) accept() {
	for {
		s, err := c.l.Accept()
		if err != nil {
			// Only the listener closing ends the loop; a handshake that
			// fails (EOF from a probe, a wrong code) does not.
			if errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) {
				return
			}
			if errors.Is(err, io.EOF) {
				continue // a spare path the Agent tried and dropped
			}
			c.ui.Log("pairing failed: %v", err)
			continue
		}
		c.mu.Lock()
		a := c.agents[s.ID]
		if a == nil {
			a = &link{id: s.ID, pending: map[string]*pend{}, streams: map[string]chan proto.Msg{}}
			c.agents[s.ID] = a
		}
		a.mu.Lock()
		if a.conn != nil {
			// The Agent moved to another path: answers on the old link are
			// lost, so the waiting commands go again on the new one.
			a.conn.Close()
			a.dropPending(false)
		}
		a.name, a.role, a.conn = s.AgentName, s.Role, s.Conn
		a.path = pathOf(s.Conn)
		a.resend(s.Conn)
		a.mu.Unlock()
		c.mu.Unlock()
		s.Conn.Send(c.addrsMsg())
		if s.Resumed {
			c.ui.Log("%s reconnected", s.AgentName)
		} else {
			c.ui.Log("paired with %s as %s", s.AgentName, s.Role)
		}
		go c.read(a, s.Conn)
		c.pushAgents()
	}
}

func pathOf(conn *secure.Conn) string {
	if conn.LocalAddr().Network() == "relay" {
		return netif.Relay
	}
	if ta, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		return netif.KindOf(ta.IP)
	}
	return netif.Other
}

func (c *Controller) addrsMsg() proto.Msg {
	b, _ := json.Marshal(c.l.Addrs())
	return proto.Msg{V: proto.Version, T: proto.TAddrs, Data: b}
}

func isClosed(err error) bool {
	return err != nil && (errors.Is(err, io.EOF) || containsClosed(err.Error()))
}

func containsClosed(s string) bool {
	return strings.Contains(s, "use of closed network connection")
}

func (c *Controller) read(a *link, conn *secure.Conn) {
	defer func() {
		a.mu.Lock()
		if a.conn == conn {
			a.conn = nil
			a.dropPending(false)
		}
		a.mu.Unlock()
		conn.Close()
		c.pushAgents()
	}()
	for {
		conn.SetReadDeadline(time.Now().Add(LostAfter))
		raw, err := conn.Recv()
		if err != nil {
			return
		}
		var m proto.Msg
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		switch m.T {
		case proto.TState:
			var st proto.State
			if json.Unmarshal(m.Data, &st) == nil {
				a.mu.Lock()
				a.state = &st
				a.mu.Unlock()
				c.pushAgents()
			}
		case proto.TEvent:
			var ev proto.Event
			if json.Unmarshal(m.Data, &ev) == nil {
				c.ui.Event(a.id, ev)
			}
		case proto.TOK, proto.TError:
			a.mu.Lock()
			pd := a.pending[m.ID]
			delete(a.pending, m.ID)
			st := a.streams[m.ID]
			if m.T == proto.TError {
				delete(a.streams, m.ID)
			}
			a.mu.Unlock()
			if pd != nil {
				if pd.resent && m.T == proto.TError && m.Code == proto.ErrStale {
					// It ran before the link dropped; only the answer was lost.
					m = proto.Msg{V: m.V, T: proto.TOK, ID: m.ID, Cmd: m.Cmd}
				}
				pd.ch <- m
			} else if st != nil && m.T == proto.TError {
				st <- m
				close(st)
			}
		case proto.TFileData:
			a.mu.Lock()
			st := a.streams[m.ID]
			a.mu.Unlock()
			if st != nil {
				st <- m
			}
		case proto.TBye:
			a.mu.Lock()
			c.mu.Lock()
			delete(c.agents, a.id)
			c.mu.Unlock()
			a.mu.Unlock()
			c.l.Forget(a.id)
			c.ui.Log("%s closed the connection", a.name)
			return
		}
	}
}

func (c *Controller) pinger() {
	for {
		time.Sleep(PingEvery)
		am := c.addrsMsg()
		c.mu.Lock()
		changed := string(am.Data) != c.addrs
		c.addrs = string(am.Data)
		links := make([]*link, 0, len(c.agents))
		for _, a := range c.agents {
			links = append(links, a)
		}
		c.mu.Unlock()
		for _, a := range links {
			a.mu.Lock()
			if changed && a.conn != nil {
				go a.conn.Send(am)
			}
			for id, p := range a.pending {
				if !p.lostAt.IsZero() && time.Since(p.lostAt) > HoldFor {
					close(p.ch)
					delete(a.pending, id)
				}
			}
			a.mu.Unlock()
		}
		for _, a := range links {
			go func(a *link) {
				t0 := time.Now()
				if _, err := c.callLink(a, proto.CmdPing, proto.PingArgs{T0: t0.UnixMilli()}, false); err == nil {
					a.mu.Lock()
					a.latency = time.Since(t0)
					a.mu.Unlock()
				}
			}(a)
		}
		c.pushAgents()
	}
}

// Agents lists the paired Agents.
func (c *Controller) Agents() []AgentInfo {
	c.mu.Lock()
	links := make([]*link, 0, len(c.agents))
	for _, a := range c.agents {
		links = append(links, a)
	}
	c.mu.Unlock()
	out := make([]AgentInfo, 0, len(links))
	for _, a := range links {
		a.mu.Lock()
		ai := AgentInfo{ID: a.id, Name: a.name, Role: a.role, Online: a.conn != nil, LatencyMs: a.latency.Milliseconds(), State: a.state}
		if a.conn != nil {
			ai.Path = a.path
		}
		if a.state != nil && a.state.Holder != nil && a.state.Holder.ID == a.id {
			ai.Holder = true
		}
		a.mu.Unlock()
		out = append(out, ai)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (c *Controller) pushAgents() { c.ui.Agents(c.Agents()) }

func (c *Controller) get(id string) (*link, error) {
	c.mu.Lock()
	a := c.agents[id]
	c.mu.Unlock()
	if a == nil {
		return nil, errors.New("unknown Agent")
	}
	return a, nil
}

// CallError is an error answer from the Agent.
type CallError struct{ Code, Msg string }

func (e *CallError) Error() string { return e.Code + ": " + e.Msg }

// Call sends a command and waits for the answer's data.
func (c *Controller) Call(agentID, cmd string, args any) (json.RawMessage, error) {
	a, err := c.get(agentID)
	if err != nil {
		return nil, err
	}
	return c.callLink(a, cmd, args, proto.NeedsHolder(cmd))
}

func (c *Controller) callLink(a *link, cmd string, args any, withSeq bool) (json.RawMessage, error) {
	m, err := c.send(a, cmd, args, withSeq, nil)
	if err != nil {
		return nil, err
	}
	return c.wait(a, m)
}

type sent struct {
	id string
	ch chan proto.Msg
}

func (c *Controller) send(a *link, cmd string, args any, withSeq bool, stream chan proto.Msg) (sent, error) {
	id := strconv.FormatUint(c.idn.Add(1), 36)
	m := proto.Msg{V: proto.Version, T: proto.TCmd, ID: id, Cmd: cmd}
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			return sent{}, err
		}
		m.Args = b
	}
	ch := make(chan proto.Msg, 1)
	a.mu.Lock()
	conn := a.conn
	if conn == nil {
		a.mu.Unlock()
		return sent{}, errors.New("the Agent is offline, reconnecting")
	}
	if withSeq {
		a.seq++
		m.Seq = a.seq
	}
	a.pending[id] = &pend{ch: ch, msg: m}
	if stream != nil {
		a.streams[id] = stream
	}
	a.mu.Unlock()
	if err := conn.Send(m); err != nil {
		a.mu.Lock()
		delete(a.pending, id)
		delete(a.streams, id)
		a.mu.Unlock()
		return sent{}, err
	}
	return sent{id, ch}, nil
}

func (c *Controller) wait(a *link, s sent) (json.RawMessage, error) {
	select {
	case r, ok := <-s.ch:
		if !ok {
			return nil, errors.New("the connection dropped")
		}
		if r.T == proto.TError {
			return nil, &CallError{Code: r.Code, Msg: r.Err}
		}
		return r.Data, nil
	case <-time.After(CallTimeout):
		a.mu.Lock()
		delete(a.pending, s.id)
		a.mu.Unlock()
		return nil, errors.New("no answer from the Agent")
	}
}

// Take asks for the control key.
func (c *Controller) Take(agentID string) error {
	_, err := c.Call(agentID, proto.CmdControlTake, nil)
	return err
}

// Release gives the key back.
func (c *Controller) Release(agentID string) error {
	_, err := c.Call(agentID, proto.CmdControlFree, nil)
	return err
}

// Forget closes the link to an Agent and drops its key.
func (c *Controller) Forget(agentID string) {
	c.mu.Lock()
	a := c.agents[agentID]
	delete(c.agents, agentID)
	c.mu.Unlock()
	c.l.Forget(agentID)
	if a != nil {
		a.mu.Lock()
		if a.conn != nil {
			a.conn.Send(proto.Msg{V: proto.Version, T: proto.TBye})
			a.conn.Close()
		}
		a.mu.Unlock()
	}
	c.pushAgents()
}

func fileSHA(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// Upload sends a local file to rel on the Agent, resuming where a previous
// attempt stopped. It returns the final name on the Agent.
func (c *Controller) Upload(agentID, local, rel string) (string, error) {
	sum, size, err := fileSHA(local)
	if err != nil {
		return "", err
	}
	pr := Progress{Agent: agentID, Path: rel, Total: size, Up: true, State: "running"}
	report := func(err error) {
		if err != nil {
			pr.State, pr.Err = "error", err.Error()
		}
		c.ui.Progress(pr)
	}
	for attempt := 0; ; attempt++ {
		final, err := c.uploadOnce(agentID, local, rel, sum, size, &pr)
		if err == nil {
			pr.Path, pr.Done, pr.State = final, size, "ok"
			report(nil)
			return final, nil
		}
		var ce *CallError
		if errors.As(err, &ce) || attempt >= 30 {
			report(err)
			return "", err
		}
		// Link dropped: wait for the Agent to reconnect and resume.
		time.Sleep(time.Second)
	}
}

func (c *Controller) uploadOnce(agentID, local, rel, sum string, size int64, pr *Progress) (string, error) {
	raw, err := c.Call(agentID, proto.CmdFileOffer, proto.OfferArgs{Path: rel, Size: size, SHA256: sum})
	if err != nil {
		return "", err
	}
	var or proto.OfferReply
	if err := json.Unmarshal(raw, &or); err != nil {
		return "", err
	}
	f, err := os.Open(local)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, proto.ChunkSize)
	off := or.Offset
	last := time.Now()
	for off < size {
		n, err := f.ReadAt(buf, off)
		if n == 0 && err != nil {
			return "", err
		}
		if _, err := c.Call(agentID, proto.CmdFileChunk, proto.ChunkArgs{ID: or.ID, Offset: off, Data: buf[:n]}); err != nil {
			return "", err
		}
		off += int64(n)
		if time.Since(last) > 200*time.Millisecond {
			pr.Done = off
			c.ui.Progress(*pr)
			last = time.Now()
		}
	}
	raw, err = c.Call(agentID, proto.CmdFileDone, proto.DoneArgs{ID: or.ID})
	if err != nil {
		return "", err
	}
	var pa proto.PathArgs
	json.Unmarshal(raw, &pa)
	return pa.Path, nil
}

// UploadDir sends a local folder file by file, keeping its structure.
func (c *Controller) UploadDir(agentID, localDir, relBase string) error {
	var first error
	err := filepath.WalkDir(localDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		r, _ := filepath.Rel(localDir, p)
		dst := path.Join(relBase, filepath.ToSlash(r))
		if _, err := c.Upload(agentID, p, dst); err != nil && first == nil {
			first = fmt.Errorf("%s: %w", dst, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return first
}

// Download fetches rel from the Agent into the local path, checking the hash.
func (c *Controller) Download(agentID, rel, local string) error {
	a, err := c.get(agentID)
	if err != nil {
		return err
	}
	tmp := local + ".waymark-part"
	var off int64
	if st, err := os.Stat(tmp); err == nil {
		off = st.Size()
	}
	stream := make(chan proto.Msg, 64)
	s, err := c.send(a, proto.CmdFileGet, proto.GetArgs{Path: rel, Offset: off}, false, stream)
	if err != nil {
		return err
	}
	raw, err := c.wait(a, s)
	if err != nil {
		a.mu.Lock()
		delete(a.streams, s.id)
		a.mu.Unlock()
		return err
	}
	var gr proto.GetReply
	json.Unmarshal(raw, &gr)
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	pr := Progress{Agent: agentID, Path: rel, Total: gr.Size, Done: off, State: "running"}
	defer func() {
		a.mu.Lock()
		delete(a.streams, s.id)
		a.mu.Unlock()
	}()
	last := time.Now()
	for {
		var m proto.Msg
		var ok bool
		select {
		case m, ok = <-stream:
		case <-time.After(CallTimeout):
			f.Close()
			return errors.New("download stalled")
		}
		if !ok {
			f.Close()
			return errors.New("the connection dropped; download again to resume")
		}
		if m.T == proto.TError {
			f.Close()
			return &CallError{Code: m.Code, Msg: m.Err}
		}
		var d proto.FileData
		if err := json.Unmarshal(m.Data, &d); err != nil {
			f.Close()
			return err
		}
		if _, err := f.WriteAt(d.Data, d.Offset); err != nil {
			f.Close()
			return err
		}
		pr.Done = d.Offset + int64(len(d.Data))
		if time.Since(last) > 200*time.Millisecond {
			c.ui.Progress(pr)
			last = time.Now()
		}
		if d.Last {
			break
		}
	}
	f.Close()
	sum, _, err := fileSHA(tmp)
	if err != nil {
		return err
	}
	if sum != gr.SHA256 {
		os.Remove(tmp)
		pr.State, pr.Err = "error", "damaged in transfer"
		c.ui.Progress(pr)
		return &CallError{Code: proto.ErrHashMismatch, Msg: "the file arrived damaged, download it again"}
	}
	final := uniqueLocal(local)
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	pr.State, pr.Path = "ok", rel
	c.ui.Progress(pr)
	return nil
}

func uniqueLocal(p string) string {
	ext := filepath.Ext(p)
	stem := p[:len(p)-len(ext)]
	for n := 1; ; n++ {
		cand := p
		if n > 1 {
			cand = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		if _, err := os.Lstat(cand); errors.Is(err, os.ErrNotExist) {
			return cand
		}
	}
}
