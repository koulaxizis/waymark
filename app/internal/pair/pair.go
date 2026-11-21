// Package pair connects an Agent to a Controller (flow A of the Doc).
//
// The Controller listens and shows a 12-character code. The Agent never
// listens: it dials out to the Controller, so the presenter laptop needs no
// inbound firewall rule, even on a "Public" network. Both sides run CPace with
// the secret inside the code, confirm the key, and show the same 4-digit
// number; the presenter accepts on the Agent. The key then lasts 24 hours, so
// the Agent can reconnect on its own after a network drop without a new code.
package pair

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"filippo.io/cpace"

	"github.com/koulaxizis/waymark/app/internal/code"
	"github.com/koulaxizis/waymark/app/internal/netif"
	"github.com/koulaxizis/waymark/app/internal/proto"
	"github.com/koulaxizis/waymark/app/internal/relay"
	"github.com/koulaxizis/waymark/app/internal/secure"
)

// KeyLife is how long a pairing can resume without a new code.
const KeyLife = 24 * time.Hour

// MaxFails is the number of wrong attempts before the code changes.
const MaxFails = 3

// ConfirmTimeout is how long the Controller waits for the presenter to accept.
const ConfirmTimeout = 2 * time.Minute

var ErrRejected = errors.New("the presenter did not accept the connection")

func cpaceContext() *cpace.ContextInfo {
	return cpace.NewContextInfo("waymark-agent", "waymark-controller", []byte(secure.Version+" pair"))
}

// hello is every plaintext handshake message.
type hello struct {
	T    string     `json:"t"`
	V    int        `json:"v,omitempty"`
	A    string     `json:"a,omitempty"`
	B    string     `json:"b,omitempty"`
	MAC  string     `json:"mac,omitempty"`
	N    string     `json:"n,omitempty"`
	ID   string     `json:"id,omitempty"`
	Name string     `json:"name,omitempty"`
	Role proto.Role `json:"role,omitempty"`
	Code string     `json:"code,omitempty"`
	Err  string     `json:"err,omitempty"`
}

// confirmMsg is the first encrypted message, Agent -> Controller.
type confirmMsg struct {
	T    string     `json:"t"`
	ID   string     `json:"id"`
	Role proto.Role `json:"role"` // granted role: the requested one or lower
}

// Session is an authenticated channel, as the Controller sees it.
type Session struct {
	Conn      *secure.Conn
	ID        string // pairing id, stable across reconnects
	AgentName string
	Role      proto.Role
	Resumed   bool
}

type known struct {
	key   []byte
	until time.Time
	name  string
	role  proto.Role
}

// Listener is the Controller side.
type Listener struct {
	Name string     // shown on the Agent ("Χειριστής Αίθουσας Α")
	Role proto.Role // requested role

	// OnSAS is called during a new pairing with the number to show; the
	// Controller then waits for the presenter to accept on the Agent.
	OnSAS func(sas, agentName string)

	ln         net.Listener
	port       int
	mu         sync.Mutex
	secret     uint32
	fails      int
	pairs      map[string]*known
	acceptOnce sync.Once
	results    chan accepted
	// listAddrs is ListAddrs as it was at Listen.
	listAddrs func(port int) proto.AddrsData
	// OnCode is called whenever the code changes (after a pairing or after
	// MaxFails wrong attempts).
	OnCode func()

	done      chan struct{} // closed by Close
	closeOnce sync.Once
	room      string        // this Controller's room on a relay
	relay     *relay.Config // nil: no relay
	relayKick chan struct{}
	// OnRelay reports whether the relay takes this Controller's socket:
	// nil when it does, the error when it does not. Called on changes only.
	OnRelay   func(err error)
	relayOnce sync.Once
}

// Listen opens a TCP port in 49152-65535 on every IPv4 interface.
func Listen(name string, role proto.Role) (*Listener, error) {
	if !role.Valid() {
		return nil, fmt.Errorf("unknown role %q", role)
	}
	var ln net.Listener
	var err error
	for i := 0; i < 20; i++ {
		port := 49152 + int(randUint32()%16384)
		ln, err = net.Listen("tcp4", ":"+strconv.Itoa(port))
		if err == nil {
			l := &Listener{Name: name, Role: role, ln: ln, port: port, pairs: map[string]*known{}, listAddrs: ListAddrs,
				results: make(chan accepted), done: make(chan struct{}), relayKick: make(chan struct{}, 1),
				room: hex.EncodeToString(secure.RandBytes(16))}
			l.secret = randUint32() & (1<<30 - 1)
			return l, nil
		}
	}
	return nil, err
}

func randUint32() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return binary.BigEndian.Uint32(b[:])
}

// Port is the listening port.
func (l *Listener) Port() int { return l.port }

// Close stops listening, on the network and on the relay.
func (l *Listener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return l.ln.Close()
}

// SetRelay sets the relay the Agents fall back to when no network answers
// (Proposal 13, 0.1.1); an empty url turns it off. The Agents learn it with
// the next address list.
func (l *Listener) SetRelay(url, key string) {
	l.mu.Lock()
	if url = relay.CleanURL(url); url == "" {
		l.relay = nil
	} else {
		l.relay = &relay.Config{URL: url, Key: key, Room: l.room}
	}
	l.mu.Unlock()
	l.relayOnce.Do(func() { go l.relayLoop() })
	select {
	case l.relayKick <- struct{}{}:
	default:
	}
}

// relayLoop keeps one socket waiting in this Controller's room; each Agent
// that joins gets a handshake like one over TCP, and a new socket waits.
func (l *Listener) relayLoop() {
	backoff := time.Second
	var last error = errors.New("off")
	report := func(err error) {
		if (err == nil) != (last == nil) || (err != nil && err.Error() != last.Error()) {
			last = err
			if l.OnRelay != nil {
				l.OnRelay(err)
			}
		}
	}
	for {
		l.mu.Lock()
		cfg := l.relay
		l.mu.Unlock()
		if cfg == nil {
			last = errors.New("off")
			select {
			case <-l.done:
				return
			case <-l.relayKick:
			}
			continue
		}
		// A new setting or Close stops the socket that waits now.
		stop := make(chan struct{})
		got := make(chan struct{})
		kicked := make(chan bool, 1)
		go func() {
			k := false
			select {
			case <-l.done:
			case <-l.relayKick:
				k = true
			case <-got:
			}
			kicked <- k
			close(stop)
		}()
		c, err := relay.Wait(*cfg, stop)
		close(got)
		k := <-kicked
		select {
		case <-l.done:
			if c != nil {
				c.Close()
			}
			return
		default:
		}
		if k {
			if c != nil {
				go l.serveConn(c)
			}
			backoff = time.Second
			continue
		}
		if err != nil {
			report(err)
			select {
			case <-l.done:
				return
			case <-l.relayKick:
				backoff = time.Second
			case <-time.After(backoff):
				backoff = min(backoff*2, 30*time.Second)
			}
			continue
		}
		report(nil)
		backoff = time.Second
		go l.serveConn(c)
	}
}

// Code is one way to type the Controller address.
type Code struct {
	Iface string `json:"iface"`
	Kind  string `json:"kind"` // wired, wifi, cell, other
	IP    string `json:"ip"`
	Short string `json:"short"` // XXXX-XXXX-XXXX
	Long  string `json:"long"`  // XXXX-XXXX-XXXX-XXXX
}

// Codes lists a code per interface address. The short form works whenever the
// Agent shares the first two octets with that address.
func (l *Listener) Codes() []Code {
	l.mu.Lock()
	secret := l.secret
	l.mu.Unlock()
	var out []Code
	for _, f := range netif.List() {
		pc := code.PairCode{IP: f.IP, Port: l.port, Secret: secret}
		short, err := pc.Short()
		if err != nil {
			continue
		}
		out = append(out, Code{Iface: f.Name, Kind: f.Kind, IP: f.IP.String(), Short: code.Group(short), Long: code.Group(pc.Long())})
	}
	return out
}

// Addrs is every address of this Controller, best network first, for the
// Agents to reconnect over another path when one is lost.
func (l *Listener) Addrs() proto.AddrsData {
	d := l.listAddrs(l.port)
	l.mu.Lock()
	if l.relay != nil {
		r := *l.relay
		d.Relay = &r
	}
	l.mu.Unlock()
	return d
}

// ListAddrs builds the address list; tests replace it.
var ListAddrs = func(port int) proto.AddrsData {
	d := proto.AddrsData{Port: port}
	for _, f := range netif.List() {
		d.Addrs = append(d.Addrs, proto.Addr{IP: f.IP.String(), Kind: f.Kind})
	}
	return d
}

// NewCode replaces the secret, so an old code stops working.
func (l *Listener) NewCode() {
	l.mu.Lock()
	l.secret = randUint32() & (1<<30 - 1)
	l.fails = 0
	l.mu.Unlock()
	if l.OnCode != nil {
		l.OnCode()
	}
}

// Forget drops a pairing, so that Agent needs a new code.
func (l *Listener) Forget(id string) {
	l.mu.Lock()
	delete(l.pairs, id)
	l.mu.Unlock()
}

type accepted struct {
	s   *Session
	err error
}

// Accept waits for the next Agent and its handshake. Errors from one Agent
// are returned too; the caller logs them and calls Accept again. Handshakes
// run side by side, so a slow or silent connection never holds up a
// reconnect on another path.
func (l *Listener) Accept() (*Session, error) {
	l.acceptOnce.Do(func() {
		go func() {
			for {
				c, err := l.ln.Accept()
				if err != nil {
					l.closeOnce.Do(func() { close(l.done) })
					return
				}
				go l.serveConn(c)
			}
		}()
	})
	select {
	case r := <-l.results:
		return r.s, r.err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// serveConn runs the handshake on one connection, from the network or the
// relay, and hands the result to Accept.
func (l *Listener) serveConn(c net.Conn) {
	s, err := l.handshake(c)
	if err != nil {
		c.Close()
	}
	select {
	case l.results <- accepted{s, err}:
	case <-l.done:
		if s != nil {
			s.Conn.Close()
		}
	}
}

func (l *Listener) handshake(c net.Conn) (*Session, error) {
	c.SetDeadline(time.Now().Add(10 * time.Second))
	var h hello
	if err := secure.ReadJSON(c, &h); err != nil {
		return nil, err
	}
	switch h.T {
	case "pair":
		return l.pairNew(c, h)
	case "resume":
		return l.resume(c, h)
	}
	return nil, fmt.Errorf("unexpected %q", h.T)
}

func (l *Listener) pairNew(c net.Conn, h hello) (*Session, error) {
	if h.V != proto.Version {
		secure.WriteJSON(c, hello{T: "err", Code: proto.ErrVersion, Err: fmt.Sprintf("protocol %d, this Controller speaks %d", h.V, proto.Version)})
		return nil, fmt.Errorf("agent protocol %d", h.V)
	}
	msgA, err := secure.UnB64(h.A)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	secret := l.secret
	l.mu.Unlock()
	msgB, key, err := cpace.Exchange(strconv.FormatUint(uint64(secret), 10), cpaceContext(), msgA)
	if err != nil {
		return nil, err
	}
	if err := secure.WriteJSON(c, hello{T: "pair2", B: secure.B64(msgB), MAC: secure.B64(secure.MAC(key, []byte("confirm-C"))), Name: l.Name, Role: l.Role}); err != nil {
		return nil, err
	}
	var h3 hello
	if err := secure.ReadJSON(c, &h3); err != nil {
		return nil, l.fail(fmt.Errorf("no key confirmation (wrong code?): %w", err))
	}
	mac, _ := secure.UnB64(h3.MAC)
	if h3.T != "pair3" || !secure.Equal(mac, secure.MAC(key, []byte("confirm-A"))) {
		return nil, l.fail(errors.New("wrong code"))
	}
	// The code is right: it is used up, whatever the presenter answers.
	l.NewCode()
	if l.OnSAS != nil {
		l.OnSAS(secure.SAS(key), h.Name)
	}
	c.SetDeadline(time.Now().Add(ConfirmTimeout))
	sc := secure.NewConn(c, secure.Derive(key, "session", nil), false)
	raw, err := sc.Recv()
	if err != nil {
		return nil, ErrRejected
	}
	var cm confirmMsg
	if err := json.Unmarshal(raw, &cm); err != nil || cm.T != "confirm" {
		return nil, ErrRejected
	}
	if !cm.Role.Valid() || cm.Role.Rank() > l.Role.Rank() {
		return nil, errors.New("agent granted an invalid role")
	}
	c.SetDeadline(time.Time{})
	id := pairID(key)
	l.mu.Lock()
	l.pairs[id] = &known{key: key, until: time.Now().Add(KeyLife), name: h.Name, role: cm.Role}
	l.mu.Unlock()
	return &Session{Conn: sc, ID: id, AgentName: h.Name, Role: cm.Role}, nil
}

func (l *Listener) fail(err error) error {
	l.mu.Lock()
	l.fails++
	n := l.fails
	l.mu.Unlock()
	if n >= MaxFails {
		l.NewCode()
	}
	return err
}

func (l *Listener) resume(c net.Conn, h hello) (*Session, error) {
	l.mu.Lock()
	k := l.pairs[h.ID]
	l.mu.Unlock()
	if k == nil || time.Now().After(k.until) {
		secure.WriteJSON(c, hello{T: "err", Err: "unknown pairing, a new code is needed"})
		return nil, errors.New("resume: unknown pairing")
	}
	nA, err := secure.UnB64(h.N)
	if err != nil || len(nA) != 16 {
		return nil, errors.New("resume: bad nonce")
	}
	nB := secure.RandBytes(16)
	if err := secure.WriteJSON(c, hello{T: "resume2", N: secure.B64(nB), MAC: secure.B64(secure.MAC(k.key, []byte("resume-C"), nA, nB))}); err != nil {
		return nil, err
	}
	var h3 hello
	if err := secure.ReadJSON(c, &h3); err != nil {
		return nil, err
	}
	mac, _ := secure.UnB64(h3.MAC)
	if h3.T != "resume3" || !secure.Equal(mac, secure.MAC(k.key, []byte("resume-A"), nA, nB)) {
		return nil, errors.New("resume: agent failed authentication")
	}
	c.SetDeadline(time.Time{})
	sc := secure.NewConn(c, secure.Derive(k.key, "session", append(nA, nB...)), false)
	return &Session{Conn: sc, ID: h.ID, AgentName: k.name, Role: k.role, Resumed: true}, nil
}

func pairID(key []byte) string { return secure.B64(secure.MAC(key, []byte("pair-id"))[:9]) }

// Pairing is what the Agent keeps to reconnect without a code.
type Pairing struct {
	ID       string
	Key      []byte
	Until    time.Time
	PeerName string
	Role     proto.Role

	mu    sync.Mutex
	addrs []proto.Addr // the Controller's addresses, best first
	port  int
	relay *relay.Config
}

// Path is one way to the Controller: its address and the kind of network.
type Path struct {
	Addr string // ip:port
	Kind string
}

// SetAddrs stores the Controller's current addresses (a TAddrs message).
func (p *Pairing) SetAddrs(d proto.AddrsData) {
	if d.Port <= 0 || d.Port > 65535 || len(d.Addrs) == 0 {
		return
	}
	p.mu.Lock()
	p.port = d.Port
	p.addrs = append([]proto.Addr(nil), d.Addrs...)
	p.relay = nil
	if d.Relay.Valid() {
		r := *d.Relay
		p.relay = &r
	}
	p.mu.Unlock()
}

// RelayAddr is the Addr of the relay path in Paths.
const RelayAddr = "relay"

// Paths lists where to try, best first.
func (p *Pairing) Paths() []Path {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Path, 0, len(p.addrs))
	for _, a := range p.addrs {
		if ip := net.ParseIP(a.IP).To4(); ip != nil {
			out = append(out, Path{Addr: net.JoinHostPort(ip.String(), strconv.Itoa(p.port)), Kind: a.Kind})
		}
	}
	if p.relay != nil {
		out = append(out, Path{Addr: RelayAddr, Kind: netif.Relay})
	}
	return out
}

func (p *Pairing) relayCfg() *relay.Config {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.relay
}

// Prompt asks the presenter to accept: it shows the number, who asks and in
// which role, and returns the role to grant (the requested one or lower) or
// false to refuse.
type Prompt func(sas, peerName string, role proto.Role) (proto.Role, bool)

// Dial is the Agent side of a new pairing: it parses the typed code, dials
// every candidate address at once, and keeps the first that answers.
func Dial(typed, agentName string, prompt Prompt) (*Pairing, *secure.Conn, error) {
	p, err := code.ParsePairCode(typed)
	if err != nil {
		return nil, nil, err
	}
	conn, addr, err := dialAny(p.Candidates(), 3*time.Second)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot reach the Controller: %w", err)
	}
	msgA, st, err := cpace.Start(strconv.FormatUint(uint64(p.Secret), 10), cpaceContext())
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := secure.WriteJSON(conn, hello{T: "pair", V: proto.Version, A: secure.B64(msgA), Name: agentName}); err != nil {
		conn.Close()
		return nil, nil, err
	}
	var r hello
	if err := secure.ReadJSON(conn, &r); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("no answer from the Controller: %w", err)
	}
	if r.T == "err" {
		conn.Close()
		if r.Code == proto.ErrVersion {
			return nil, nil, fmt.Errorf("Agent and Controller need the same version: %s", r.Err)
		}
		return nil, nil, errors.New(r.Err)
	}
	msgB, _ := secure.UnB64(r.B)
	key, err := st.Finish(msgB)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	macB, _ := secure.UnB64(r.MAC)
	if r.T != "pair2" || !secure.Equal(macB, secure.MAC(key, []byte("confirm-C"))) {
		conn.Close()
		return nil, nil, errors.New("wrong code")
	}
	if err := secure.WriteJSON(conn, hello{T: "pair3", MAC: secure.B64(secure.MAC(key, []byte("confirm-A")))}); err != nil {
		conn.Close()
		return nil, nil, err
	}
	if !r.Role.Valid() {
		conn.Close()
		return nil, nil, errors.New("controller sent an unknown role")
	}
	conn.SetDeadline(time.Now().Add(ConfirmTimeout))
	granted, ok := prompt(secure.SAS(key), r.Name, r.Role)
	if !ok {
		conn.Close()
		return nil, nil, ErrRejected
	}
	if !granted.Valid() || granted.Rank() > r.Role.Rank() {
		granted = r.Role
	}
	conn.SetDeadline(time.Time{})
	id := pairID(key)
	sc := secure.NewConn(conn, secure.Derive(key, "session", nil), true)
	if err := sc.Send(confirmMsg{T: "confirm", ID: id, Role: granted}); err != nil {
		conn.Close()
		return nil, nil, err
	}
	host, _, _ := net.SplitHostPort(addr)
	return &Pairing{ID: id, Key: key, Until: time.Now().Add(KeyLife), PeerName: r.Name, Role: granted,
		port: p.Port, addrs: []proto.Addr{{IP: host, Kind: netif.Other}}}, sc, nil
}

// PreferWait is how long a reconnect waits for a better path once a worse
// one has answered: a cable that answers a moment after Wi-Fi still wins.
var PreferWait = 300 * time.Millisecond

// DialTimeout bounds each connection attempt.
var DialTimeout = 1500 * time.Millisecond

// Resume re-authenticates with the stored key over the best path that
// answers; no code needed. It returns the path it used.
func Resume(p *Pairing) (*secure.Conn, Path, error) {
	return ResumeOver(p, p.Paths())
}

// ResumeOver is Resume limited to some paths (best first), for example to
// move back to a cable while Wi-Fi still works.
func ResumeOver(p *Pairing, paths []Path) (*secure.Conn, Path, error) {
	if time.Now().After(p.Until) {
		return nil, Path{}, errors.New("pairing expired, a new code is needed")
	}
	if len(paths) == 0 {
		return nil, Path{}, errors.New("no address of the Controller is known")
	}
	conn, i, err := dialBest(paths, p.relayCfg())
	if err != nil {
		return nil, Path{}, err
	}
	sc, err := resumeOn(p, conn)
	if err != nil {
		return nil, Path{}, err
	}
	return sc, paths[i], nil
}

// dialBest dials every path at once and keeps the best one that answers,
// waiting PreferWait after the first answer for a better one. The relay is
// dialled only once every network path has failed or DialTimeout is over,
// so it never wins over a network that is merely slow.
func dialBest(paths []Path, rc *relay.Config) (net.Conn, int, error) {
	type res struct {
		c net.Conn
		i int
		e error
	}
	ch := make(chan res, len(paths))
	locals := 0
	for _, pt := range paths {
		if pt.Kind != netif.Relay {
			locals++
		}
	}
	var lmu sync.Mutex
	localsFailed := make(chan struct{})
	failed := func() {
		lmu.Lock()
		if locals--; locals == 0 {
			close(localsFailed)
		}
		lmu.Unlock()
	}
	if locals == 0 {
		close(localsFailed)
	}
	for i, pt := range paths {
		go func(i int, pt Path) {
			if pt.Kind == netif.Relay {
				select {
				case <-localsFailed:
				case <-time.After(DialTimeout):
				}
				if rc == nil {
					ch <- res{nil, i, errors.New("relay not set")}
					return
				}
				c, err := relay.Dial(*rc, "a", 2*DialTimeout+5*time.Second)
				ch <- res{c, i, err}
				return
			}
			c, err := net.DialTimeout("tcp4", pt.Addr, DialTimeout)
			if err != nil {
				failed()
			}
			ch <- res{c, i, err}
		}(i, pt)
	}
	var best *res
	var first error
	var grace <-chan time.Time
	left := len(paths)
wait:
	for left > 0 {
		select {
		case r := <-ch:
			left--
			switch {
			case r.e != nil:
				if first == nil {
					first = r.e
				}
			case best == nil || r.i < best.i:
				if best != nil {
					best.c.Close()
				}
				rr := r
				best = &rr
				if best.i == 0 {
					break wait
				}
				if grace == nil {
					grace = time.After(PreferWait)
				}
			default:
				r.c.Close()
			}
		case <-grace:
			break wait
		}
	}
	// Close whatever answers after the choice.
	go func(n int) {
		for ; n > 0; n-- {
			if r := <-ch; r.e == nil {
				r.c.Close()
			}
		}
	}(left)
	if best == nil {
		return nil, 0, first
	}
	return best.c, best.i, nil
}

func resumeOn(p *Pairing, conn net.Conn) (*secure.Conn, error) {
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	nA := secure.RandBytes(16)
	if err := secure.WriteJSON(conn, hello{T: "resume", ID: p.ID, N: secure.B64(nA)}); err != nil {
		conn.Close()
		return nil, err
	}
	var r hello
	if err := secure.ReadJSON(conn, &r); err != nil || r.T != "resume2" {
		conn.Close()
		if err == nil {
			err = errors.New(r.Err)
		}
		return nil, fmt.Errorf("resume refused: %w", err)
	}
	nB, _ := secure.UnB64(r.N)
	mac, _ := secure.UnB64(r.MAC)
	if !secure.Equal(mac, secure.MAC(p.Key, []byte("resume-C"), nA, nB)) {
		conn.Close()
		return nil, errors.New("resume: controller failed authentication")
	}
	if err := secure.WriteJSON(conn, hello{T: "resume3", MAC: secure.B64(secure.MAC(p.Key, []byte("resume-A"), nA, nB))}); err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	return secure.NewConn(conn, secure.Derive(p.Key, "session", append(nA, nB...)), true), nil
}

func dialAny(addrs []string, timeout time.Duration) (net.Conn, string, error) {
	if len(addrs) == 0 {
		return nil, "", errors.New("no network address to try: is this laptop on a network?")
	}
	type res struct {
		c net.Conn
		a string
		e error
	}
	ch := make(chan res, len(addrs))
	for _, a := range addrs {
		go func(a string) {
			c, err := net.DialTimeout("tcp4", a, timeout)
			ch <- res{c, a, err}
		}(a)
	}
	var first error
	var win *res
	for range addrs {
		r := <-ch
		switch {
		case r.e == nil && win == nil:
			rr := r
			win = &rr
		case r.e == nil:
			r.c.Close()
		case first == nil:
			first = r.e
		}
	}
	if win != nil {
		return win.c, win.a, nil
	}
	return nil, "", first
}
