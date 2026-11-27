// Package agent is the Waymark Remote Agent: it runs on the presenter laptop,
// dials out to Controllers with a typed code, and runs their commands on its
// folder and on the open presentation. It checks each command against the
// sender's role and against who holds the control key.
package agent

import (
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/koulaxizis/waymark/app/internal/files"
	"github.com/koulaxizis/waymark/app/internal/netif"
	"github.com/koulaxizis/waymark/app/internal/pair"
	"github.com/koulaxizis/waymark/app/internal/pres"
	"github.com/koulaxizis/waymark/app/internal/proto"
)

// Timings (Proposal 13): state every 5 s, the Controller pings every 1 s, a
// link is lost after 3 s of silence, retry every 1 s over every known path,
// look for a better path (cable over Wi-Fi) every 5 s, the key passes on
// after the holder is gone for 10 s.
var (
	StateEvery   = 5 * time.Second
	LostAfter    = 3 * time.Second
	RetryEvery   = time.Second
	UpgradeEvery = 5 * time.Second
	HolderGrace  = 10 * time.Second
	downloadPace = time.Duration(0)
)

// UI is what the window of the Agent receives. All callbacks may come from any
// goroutine.
type UI interface {
	// Prompt asks the presenter to accept a new connection.
	Prompt(sas, peerName string, role proto.Role) (proto.Role, bool)
	State(proto.State)
	Event(proto.Event)
	Log(format string, args ...any)
}

// Agent is one running Agent.
type Agent struct {
	Name    string
	Version string

	store *files.Store
	pres  *pres.Manager
	ui    UI

	mu      sync.Mutex
	peers   map[string]*peer // by pairing id
	holder  string           // pairing id, "" when free
	lostAt  map[string]time.Time
	fileSHA string
	closed  bool
	wake    chan struct{}

	lastAddrs string // this laptop's addresses at the last tick (ticker only)
}

func addrList() string {
	var b []byte
	for _, f := range netif.List() {
		b = append(b, f.IP.String()+" "+f.Kind+";"...)
	}
	return string(b)
}

// Link is one message pipe to a Controller: the encrypted TCP link of flow A
// (secure.Conn) or the browser data channel of flow B (rtc.Link).
type Link interface {
	Send(v any) error
	Recv() ([]byte, error)
	SetReadDeadline(time.Time) error
	Close() error
}

// Resumer brings a browser Controller back after its link failed for good,
// without a new QR (rtc.Resume).
type Resumer interface {
	Wait(stop <-chan struct{}) (Link, error)
	TryCode(code string) bool
	Port() int
}

type peer struct {
	info    proto.Peer
	resume  Resumer       // a browser that can come back, else nil
	pairing *pair.Pairing // nil for a browser: it cannot resume, it pairs again
	conn    Link          // nil while reconnecting
	path    pair.Path     // the network conn runs over (flow A)
	lastSeq uint64
	stop    chan struct{}
}

// New builds an Agent on a folder and a presentation manager.
func New(name, version string, store *files.Store, pm *pres.Manager, ui UI) *Agent {
	a := &Agent{Name: name, Version: version, store: store, pres: pm, ui: ui,
		peers: map[string]*peer{}, lostAt: map[string]time.Time{}, wake: make(chan struct{}, 1)}
	go a.ticker()
	return a
}

// Store and Pres expose the parts the UI shows.
func (a *Agent) Store() *files.Store { return a.store }
func (a *Agent) Pres() *pres.Manager { return a.pres }

// Connect pairs with a Controller from a typed code and keeps the link up
// until DisconnectAll or 24 hours.
func (a *Agent) Connect(code string) error {
	pr, conn, err := pair.Dial(code, a.Name, a.ui.Prompt)
	if err != nil {
		return err
	}
	p := &peer{info: proto.Peer{ID: pr.ID, Name: pr.PeerName, Role: pr.Role}, pairing: pr, conn: conn, stop: make(chan struct{})}
	if ps := pr.Paths(); len(ps) > 0 {
		p.path = ps[0]
	}
	a.mu.Lock()
	if old := a.peers[pr.ID]; old != nil {
		close(old.stop)
		if old.conn != nil {
			old.conn.Close()
		}
	}
	a.peers[pr.ID] = p
	a.mu.Unlock()
	a.ui.Log("connected: %s (%s)", pr.PeerName, pr.Role)
	go a.keep(p)
	go a.upgrade(p)
	a.changed()
	return nil
}

// Attach adds a Controller that paired some other way, such as a browser
// over flow B. With a Resumer the browser can come back after its link
// fails; without one it is dropped and pairs again with a new QR.
func (a *Agent) Attach(id, name string, role proto.Role, l Link, res Resumer) {
	p := &peer{info: proto.Peer{ID: id, Name: name, Role: role}, conn: l, resume: res, stop: make(chan struct{})}
	a.mu.Lock()
	if old := a.peers[id]; old != nil {
		close(old.stop)
		if old.conn != nil {
			old.conn.Close()
		}
	}
	a.peers[id] = p
	a.mu.Unlock()
	a.ui.Log("connected: %s (%s, browser)", name, role)
	a.sendAddrs(p, l)
	go a.keep(p)
	a.changed()
}

// ResumeCode passes a return code typed on the Agent to the browser it
// belongs to, when that browser is trying to come back through a firewall.
func (a *Agent) ResumeCode(code string) bool {
	a.mu.Lock()
	var rs []Resumer
	for _, p := range a.peers {
		if p.resume != nil {
			rs = append(rs, p.resume)
		}
	}
	a.mu.Unlock()
	for _, r := range rs {
		if r.TryCode(code) {
			return true
		}
	}
	return false
}

// sendAddrs tells a browser where this Agent is now, for its next resume.
func (a *Agent) sendAddrs(p *peer, c Link) {
	if p.resume == nil || c == nil {
		return
	}
	d := proto.AddrsData{Port: p.resume.Port()}
	for _, f := range netif.List() {
		d.Addrs = append(d.Addrs, proto.Addr{IP: f.IP.String(), Kind: f.Kind})
	}
	c.Send(proto.Msg{V: proto.Version, T: proto.TAddrs, Data: mustJSON(d)})
}

func linkDone(c Link) bool {
	d, ok := c.(interface{ Done() <-chan struct{} })
	if !ok {
		return true
	}
	select {
	case <-d.Done():
		return true
	default:
		return false
	}
}

// keep serves the link and, after a loss, reconnects every RetryEvery over
// every path it knows (cable, Wi-Fi, mobile data), best first. When upgrade
// moves the link to a better path, keep simply goes on with the new one.
func (a *Agent) keep(p *peer) {
	a.mu.Lock()
	c := p.conn
	a.mu.Unlock()
	for {
		err := a.serve(p, c)
		select {
		case <-p.stop:
			return
		default:
		}
		a.mu.Lock()
		if p.conn != nil && p.conn != c {
			c = p.conn // moved to a better path
			a.mu.Unlock()
			continue
		}
		a.mu.Unlock()
		if p.pairing == nil {
			if c, err = a.keepBrowser(p, c, err); err != nil {
				a.ui.Log("link to %s ended: %v", p.info.Name, err)
				a.drop(p.info.ID)
				return
			}
			continue
		}
		a.ui.Log("link to %s ended: %v", p.info.Name, err)
		c.Close()
		a.mu.Lock()
		p.conn = nil
		a.lostAt[p.info.ID] = time.Now()
		a.mu.Unlock()
		a.changed()
		c = nil
		for try := 0; c == nil; try++ {
			wait := RetryEvery
			if try == 0 {
				wait = 0 // the first try right away, on every path
			}
			select {
			case <-p.stop:
				return
			case <-time.After(wait):
			}
			nc, path, err := pair.Resume(p.pairing)
			if err != nil {
				if time.Now().After(p.pairing.Until) {
					a.drop(p.info.ID)
					return
				}
				continue
			}
			a.mu.Lock()
			select {
			case <-p.stop:
				a.mu.Unlock()
				nc.Close()
				return
			default:
			}
			p.conn, p.path, c = nc, path, nc
			delete(a.lostAt, p.info.ID)
			a.mu.Unlock()
			a.ui.Log("reconnected to %s over %s", p.info.Name, kindName(path.Kind))
			a.changed()
		}
	}
}

// keepBrowser handles a browser link that went quiet or failed: a quiet one
// may come back by itself (a short Wi-Fi drop), a failed one comes back
// through its Resumer. It returns the link to serve next, or an error when
// the browser is gone for good.
func (a *Agent) keepBrowser(p *peer, c Link, err error) (Link, error) {
	if errors.Is(err, io.EOF) { // the page said bye
		c.Close()
		return nil, err
	}
	a.mu.Lock()
	wasOn := p.conn != nil
	p.conn = nil
	if _, lost := a.lostAt[p.info.ID]; !lost {
		a.lostAt[p.info.ID] = time.Now()
	}
	a.mu.Unlock()
	if wasOn {
		a.ui.Log("%s went quiet, waiting for it", p.info.Name)
		a.changed()
	}
	if !linkDone(c) {
		return c, nil // the same connection may still come back
	}
	c.Close()
	if p.resume == nil {
		return nil, err
	}
	nc, err := p.resume.Wait(p.stop)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	select {
	case <-p.stop:
		a.mu.Unlock()
		nc.Close()
		return nil, errors.New("stopped")
	default:
	}
	p.conn = nc
	p.lastSeq = 0 // a reloaded page counts from 1 again; the old link is gone
	delete(a.lostAt, p.info.ID)
	a.mu.Unlock()
	a.ui.Log("%s is back", p.info.Name)
	a.sendAddrs(p, nc)
	a.changed()
	return nc, nil
}

// upgrade moves a working link to a better path when one answers, for
// example back to the cable after the link fell over to Wi-Fi.
func (a *Agent) upgrade(p *peer) {
	for {
		select {
		case <-p.stop:
			return
		case <-time.After(UpgradeEvery):
		}
		a.mu.Lock()
		cur, rank := p.conn, netif.Rank(p.path.Kind)
		a.mu.Unlock()
		if cur == nil {
			continue
		}
		var better []pair.Path
		for _, pt := range p.pairing.Paths() {
			if netif.Rank(pt.Kind) < rank {
				better = append(better, pt)
			}
		}
		if len(better) == 0 {
			continue
		}
		nc, path, err := pair.ResumeOver(p.pairing, better)
		if err != nil {
			continue
		}
		a.mu.Lock()
		if p.conn != cur {
			a.mu.Unlock()
			nc.Close()
			continue
		}
		p.conn, p.path = nc, path
		a.mu.Unlock()
		// The Controller switches when the new link arrives; the old one
		// stays a moment so nothing in flight on it is lost.
		time.AfterFunc(time.Second, func() { cur.Close() }) // keep then goes on with the new link
		a.ui.Log("link to %s moved to %s", p.info.Name, kindName(path.Kind))
		a.changed()
	}
}

func kindName(k string) string {
	switch k {
	case netif.Wired:
		return "the cable"
	case netif.WiFi:
		return "Wi-Fi"
	case netif.Cell:
		return "mobile data"
	case netif.Relay:
		return "the relay"
	case netif.Bluetooth:
		return "Bluetooth"
	}
	return "another network"
}

func (a *Agent) serve(p *peer, c Link) error {
	if c == nil {
		return errors.New("no connection")
	}
	a.sendState(c)
	for {
		c.SetReadDeadline(time.Now().Add(LostAfter))
		raw, err := c.Recv()
		if err != nil {
			return err
		}
		a.mu.Lock()
		back := p.conn == nil && p.pairing == nil // a quiet browser speaks again
		if back {
			p.conn = c
			delete(a.lostAt, p.info.ID)
		}
		a.mu.Unlock()
		if back {
			a.ui.Log("%s is back", p.info.Name)
			a.changed()
		}
		var m proto.Msg
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		if m.T == proto.TBye {
			return io.EOF
		}
		if m.T == proto.TAddrs && p.pairing != nil {
			var d proto.AddrsData
			if json.Unmarshal(m.Data, &d) == nil {
				p.pairing.SetAddrs(d)
				moved := false
				a.mu.Lock()
				for _, pt := range p.pairing.Paths() {
					if pt.Addr == p.path.Addr && p.conn == c && pt.Kind != p.path.Kind {
						p.path.Kind, moved = pt.Kind, true
					}
				}
				a.mu.Unlock()
				if moved {
					a.changed()
				}
			}
			continue
		}
		if m.T != proto.TCmd {
			continue
		}
		reply := a.handle(p, c, m)
		if reply != nil {
			if err := c.Send(reply); err != nil {
				return err
			}
		}
	}
}

func ok(m proto.Msg, data any) *proto.Msg {
	r := &proto.Msg{V: proto.Version, T: proto.TOK, ID: m.ID, Cmd: m.Cmd}
	if data != nil {
		r.Data, _ = json.Marshal(data)
	}
	return r
}

func fail(m proto.Msg, code, msg string) *proto.Msg {
	return &proto.Msg{V: proto.Version, T: proto.TError, ID: m.ID, Cmd: m.Cmd, Code: code, Err: msg}
}

func failErr(m proto.Msg, err error) *proto.Msg {
	code := files.CodeOf(err)
	if code == proto.ErrIO {
		code = pres.CodeOf(err)
	}
	return fail(m, code, err.Error())
}

func (a *Agent) handle(p *peer, c Link, m proto.Msg) *proto.Msg {
	role := p.info.Role
	if !proto.Allowed(role, m.Cmd) {
		if m.Cmd == "" || !known(m.Cmd) {
			return fail(m, proto.ErrUnknown, "unknown command")
		}
		return fail(m, proto.ErrNotAllowed, "your role cannot do this")
	}
	if proto.NeedsHolder(m.Cmd) {
		a.mu.Lock()
		holder := a.holder
		if holder != p.info.ID {
			a.mu.Unlock()
			return fail(m, proto.ErrNotHolder, "take control first")
		}
		if m.Seq != 0 && m.Seq <= p.lastSeq {
			a.mu.Unlock()
			return fail(m, proto.ErrStale, "old command ignored")
		}
		if m.Seq != 0 {
			p.lastSeq = m.Seq
		}
		a.mu.Unlock()
	}
	switch m.Cmd {
	case proto.CmdPing:
		var args proto.PingArgs
		json.Unmarshal(m.Args, &args)
		return ok(m, args)
	case proto.CmdStateGet:
		return ok(m, a.State())
	case proto.CmdControlTake:
		return a.take(p, m)
	case proto.CmdControlFree:
		a.mu.Lock()
		if a.holder == p.info.ID {
			a.holder = ""
		}
		a.mu.Unlock()
		a.changed()
		return ok(m, nil)
	case proto.CmdFileList:
		l, err := a.store.List()
		if err != nil {
			return failErr(m, err)
		}
		return ok(m, l)
	case proto.CmdFileOffer:
		var args proto.OfferArgs
		if err := json.Unmarshal(m.Args, &args); err != nil {
			return fail(m, proto.ErrBadRequest, err.Error())
		}
		r, err := a.store.Offer(args)
		if err != nil {
			return failErr(m, err)
		}
		return ok(m, r)
	case proto.CmdFileChunk:
		var args proto.ChunkArgs
		if err := json.Unmarshal(m.Args, &args); err != nil {
			return fail(m, proto.ErrBadRequest, err.Error())
		}
		n, err := a.store.Chunk(args)
		if err != nil {
			return failErr(m, err)
		}
		return ok(m, map[string]int64{"offset": n})
	case proto.CmdFileDone:
		var args proto.DoneArgs
		json.Unmarshal(m.Args, &args)
		final, err := a.store.Done(args.ID)
		if err != nil {
			return failErr(m, err)
		}
		a.ui.Log("received %s from %s", final, p.info.Name)
		ev := proto.Event{Kind: "file.new", Path: final, By: &p.info}
		a.ui.Event(ev)
		a.broadcast(proto.Msg{V: proto.Version, T: proto.TEvent, Data: mustJSON(ev)})
		return ok(m, proto.PathArgs{Path: final})
	case proto.CmdFileGet:
		var args proto.GetArgs
		json.Unmarshal(m.Args, &args)
		return a.download(c, m, args)
	case proto.CmdFileMkdir:
		var args proto.PathArgs
		json.Unmarshal(m.Args, &args)
		r, err := a.store.Mkdir(args.Path)
		if err != nil {
			return failErr(m, err)
		}
		return ok(m, proto.PathArgs{Path: r})
	case proto.CmdFileRename:
		var args proto.RenameArgs
		json.Unmarshal(m.Args, &args)
		r, err := a.store.Rename(args.From, args.To)
		if err != nil {
			return failErr(m, err)
		}
		return ok(m, proto.PathArgs{Path: r})
	case proto.CmdFileDelete:
		var args proto.PathArgs
		json.Unmarshal(m.Args, &args)
		if err := a.store.Delete(args.Path); err != nil {
			return failErr(m, err)
		}
		a.ui.Log("%s sent %s to the Recycle Bin", p.info.Name, args.Path)
		return ok(m, nil)
	}
	return a.handlePres(m)
}

func known(cmd string) bool {
	for _, r := range []proto.Role{proto.RoleSupervisor} {
		if proto.Allowed(r, cmd) {
			return true
		}
	}
	return false
}

func (a *Agent) take(p *peer, m proto.Msg) *proto.Msg {
	a.mu.Lock()
	cur := a.peers[a.holder]
	free := a.holder == "" || cur == nil
	if !free && cur.conn == nil {
		if t, lost := a.lostAt[a.holder]; lost && time.Since(t) > HolderGrace {
			free = true
		}
	}
	switch {
	case a.holder == p.info.ID:
	case free || p.info.Role == proto.RoleSupervisor:
		prev := cur
		a.holder = p.info.ID
		if prev != nil && prev != p && prev.conn != nil {
			ev := proto.Event{Kind: "control.taken", By: &p.info}
			go prev.conn.Send(proto.Msg{V: proto.Version, T: proto.TEvent, Data: mustJSON(ev)})
		}
	default:
		name := cur.info.Name
		a.mu.Unlock()
		return fail(m, proto.ErrBusy, "control is held by "+name)
	}
	a.mu.Unlock()
	a.ui.Log("%s has control", p.info.Name)
	a.changed()
	return ok(m, nil)
}

func (a *Agent) download(c Link, m proto.Msg, args proto.GetArgs) *proto.Msg {
	f, size, sum, err := a.store.Open(args.Path)
	if err != nil {
		return failErr(m, err)
	}
	if args.Offset < 0 || args.Offset > size {
		f.Close()
		return fail(m, proto.ErrBadRequest, "bad offset")
	}
	if err := c.Send(ok(m, proto.GetReply{Size: size, SHA256: sum})); err != nil {
		f.Close()
		return nil
	}
	go func() {
		defer f.Close()
		buf := make([]byte, proto.ChunkSize)
		off := args.Offset
		for {
			n, err := f.ReadAt(buf, off)
			last := err == io.EOF || off+int64(n) >= size
			if err != nil && err != io.EOF {
				c.Send(fail(m, proto.ErrIO, err.Error()))
				return
			}
			d := proto.FileData{Offset: off, Data: buf[:n], Last: last}
			if err := c.Send(proto.Msg{V: proto.Version, T: proto.TFileData, ID: m.ID, Data: mustJSON(d)}); err != nil {
				return
			}
			off += int64(n)
			if last {
				return
			}
			if downloadPace > 0 {
				time.Sleep(downloadPace)
			}
		}
	}()
	return nil
}

func (a *Agent) handlePres(m proto.Msg) *proto.Msg {
	var err error
	switch m.Cmd {
	case proto.CmdPresOpen:
		var args proto.OpenArgs
		json.Unmarshal(m.Args, &args)
		var p string
		if p, err = a.store.Path(args.Path); err != nil {
			return failErr(m, err)
		}
		if err = a.pres.Open(p, args.App); err == nil {
			rel, _ := files.Clean(args.Path)
			a.store.SetOpen(rel)
			go a.hashOpen(rel)
		}
	case proto.CmdPresStart:
		var args proto.StartArgs
		json.Unmarshal(m.Args, &args)
		err = a.pres.Start(args.From)
		if err == nil {
			a.ui.Event(proto.Event{Kind: "pres.showing"})
		}
	case proto.CmdPresNext:
		err = a.pres.Next()
	case proto.CmdPresPrev:
		err = a.pres.Prev()
	case proto.CmdPresGoto:
		var args proto.GotoArgs
		json.Unmarshal(m.Args, &args)
		err = a.pres.Goto(args.Slide)
	case proto.CmdPresBlack:
		var args proto.BlackArgs
		json.Unmarshal(m.Args, &args)
		err = a.pres.Black(args.On)
	case proto.CmdPresEnd:
		err = a.pres.End()
	case proto.CmdPresClose:
		err = a.pres.Close()
		a.store.SetOpen("")
		a.mu.Lock()
		a.fileSHA = ""
		a.mu.Unlock()
	default:
		return fail(m, proto.ErrUnknown, "unknown command")
	}
	if err != nil {
		return failErr(m, err)
	}
	a.changed()
	return ok(m, a.State())
}

func (a *Agent) hashOpen(rel string) {
	sum, _, err := a.store.Hash(rel)
	if err != nil {
		return
	}
	a.mu.Lock()
	a.fileSHA = sum
	a.mu.Unlock()
	a.changed()
}

// State is the current report.
func (a *Agent) State() proto.State {
	file, app, st := a.pres.Current()
	a.mu.Lock()
	defer a.mu.Unlock()
	s := proto.State{Device: a.Name, Agent: a.Version, OS: runtime.GOOS, App: app,
		Slide: st.Slide, Slides: st.Slides, Status: st.State, Folder: a.store.Root(), Peers: []proto.Peer{}}
	if file != "" {
		s.File = a.store.Rel(file)
		s.FileSHA = a.fileSHA
	}
	if s.Status == "" {
		s.Status = proto.StatusIdle
	}
	for _, p := range a.peers {
		pi := p.info
		pi.Browser = p.pairing == nil
		if p.conn != nil {
			pi.Path = p.path.Kind
			s.Peers = append(s.Peers, pi)
		} else {
			s.Waiting = append(s.Waiting, pi)
		}
	}
	sort.Slice(s.Peers, func(i, j int) bool { return s.Peers[i].Name < s.Peers[j].Name })
	sort.Slice(s.Waiting, func(i, j int) bool { return s.Waiting[i].Name < s.Waiting[j].Name })
	if h := a.peers[a.holder]; h != nil {
		hi := h.info
		s.Holder = &hi
	}
	return s
}

func (a *Agent) sendState(c Link) {
	c.Send(proto.Msg{V: proto.Version, T: proto.TState, Data: mustJSON(a.State())})
}

func (a *Agent) broadcast(m proto.Msg) {
	a.mu.Lock()
	var conns []Link
	for _, p := range a.peers {
		if p.conn != nil {
			conns = append(conns, p.conn)
		}
	}
	a.mu.Unlock()
	for _, c := range conns {
		c.Send(m)
	}
}

// Changed pushes the state now, e.g. after the built-in viewer moved.
func (a *Agent) Changed() { a.changed() }

// changed pushes the state now (coalesced).
func (a *Agent) changed() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

func (a *Agent) ticker() {
	t := time.NewTicker(StateEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
		case <-a.wake:
		}
		// The holder keeps the key through a lost link; another Controller
		// can take it once the holder has been gone HolderGrace (take).
		a.mu.Lock()
		closed := a.closed
		a.mu.Unlock()
		if closed {
			return
		}
		if l := addrList(); l != a.lastAddrs {
			a.lastAddrs = l
			a.mu.Lock()
			var ps []*peer
			for _, p := range a.peers {
				ps = append(ps, p)
			}
			a.mu.Unlock()
			for _, p := range ps {
				a.mu.Lock()
				c := p.conn
				a.mu.Unlock()
				a.sendAddrs(p, c)
			}
		}
		st := a.State()
		a.ui.State(st)
		a.broadcast(proto.Msg{V: proto.Version, T: proto.TState, Data: mustJSON(st)})
	}
}

func (a *Agent) drop(id string) {
	a.mu.Lock()
	p := a.peers[id]
	delete(a.peers, id)
	delete(a.lostAt, id)
	if a.holder == id {
		a.holder = ""
	}
	a.mu.Unlock()
	if p != nil {
		select {
		case <-p.stop:
		default:
			close(p.stop)
		}
		if p.conn != nil {
			p.conn.Send(proto.Msg{V: proto.Version, T: proto.TBye})
			p.conn.Close()
		}
	}
	a.changed()
}

// Disconnect closes one link and forgets its key.
func (a *Agent) Disconnect(id string) { a.drop(id) }

// DisconnectAll is the presenter's big button: every link closes, the
// supervisor's too, and no key is kept.
func (a *Agent) DisconnectAll() {
	a.mu.Lock()
	ids := make([]string, 0, len(a.peers))
	for id := range a.peers {
		ids = append(ids, id)
	}
	a.mu.Unlock()
	for _, id := range ids {
		a.drop(id)
	}
	a.ui.Log("all connections closed")
}

// Close stops the Agent.
func (a *Agent) Close() {
	a.DisconnectAll()
	a.pres.Close()
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	a.changed()
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
