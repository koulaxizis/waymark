// controller-spike is the operator side of the Waymark pairing spike (flow A).
// It shows a 12-character code, waits for the Agent to dial in, pairs with
// CPace, opens WebRTC and measures round trips. Throwaway code.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"filippo.io/cpace"
	"github.com/koulaxizis/waymark/spike/internal/wm"
	"github.com/pion/webrtc/v4"
)

var (
	flagPort  = flag.Int("port", 0, "TCP port (49152-65535, default random)")
	flagAuto  = flag.Bool("auto", false, "no keyboard: measure, then wait for losses (automated tests)")
	flagQuit  = flag.Duration("quit-after", 0, "end the test after this long (automated tests)")
	flagCount = flag.Int("pings", 200, "pings in each measurement")
	flagUDP   = flag.Int("udp-base", 0, "use UDP ports udp-base..udp-base+99 for WebRTC (for firewall rules)")
)

func settings() webrtc.SettingEngine {
	se := wm.BaseSettings()
	if *flagUDP > 0 {
		se.SetEphemeralUDPPortRange(uint16(*flagUDP), uint16(*flagUDP+99))
	}
	return se
}

var lg *wm.Logger

type pairState struct {
	mu     sync.Mutex
	secret uint32
	fails  int
	id     string
	key    []byte
	until  time.Time
	port   int
	inUse  bool
}

var ps pairState

func main() {
	flag.Parse()
	lg = wm.NewLogger("controller")
	defer lg.Close()
	lg.Header("controller", wm.SpikeVersion)
	err := run()
	if err != nil {
		lg.Printf("ERROR %v", err)
	}
	if lg.Path != "" {
		lg.Say("\nΤο αρχείο log είναι στο / The log file is at:\n  %s\n", lg.Path)
	}
	if !*flagAuto {
		wm.WaitExit()
	}
	if err != nil {
		os.Exit(1)
	}
}

func newSecret() uint32 {
	b := wm.RandBytes(4)
	return (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) & (1<<30 - 1)
}

func listen() (net.Listener, int, error) {
	if *flagPort != 0 {
		l, err := net.Listen("tcp4", fmt.Sprintf(":%d", *flagPort))
		return l, *flagPort, err
	}
	for i := 0; i < 50; i++ {
		p := 49152 + int(wm.RandBytes(1)[0])<<6 + int(wm.RandBytes(1)[0]&63)
		if l, err := net.Listen("tcp4", fmt.Sprintf(":%d", p)); err == nil {
			return l, p, nil
		}
	}
	return nil, 0, errors.New("no free TCP port")
}

func printCodes() {
	ps.mu.Lock()
	secret, port := ps.secret, ps.port
	ps.mu.Unlock()
	lg.Say("\nΚωδικός για το Agent / Code for the Agent:\n")
	for _, a := range wm.InterfaceIPv4s() {
		ip := net.ParseIP(a[1])
		c := wm.PairCode{IP: ip, Port: port, Secret: secret}
		short, _ := c.Short()
		lg.Say("\n   %s      (%s, %s)\n", wm.Group(short), a[0], a[1])
		lg.Say("   μακρύς / long: %s\n", wm.Group(c.Long()))
	}
	lg.Say("\nΠληκτρολόγησέ τον στο Agent. / Type it on the Agent.\n")
	lg.Printf("codes shown for port %d (secret not logged)", port)
}

func run() error {
	l, port, err := listen()
	if err != nil {
		return err
	}
	ps.secret, ps.port = newSecret(), port
	lg.Printf("listening on TCP %d", port)
	printCodes()

	sessions := make(chan *wm.SecureConn)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, err := handshake(c)
				if err != nil {
					lg.Printf("connection from %s refused: %v", c.RemoteAddr(), err)
					c.Close()
					return
				}
				sessions <- sc
			}()
		}
	}()

	lines := wm.Lines()
	if *flagAuto {
		lines = nil
	}
	var deadline <-chan time.Time
	if *flagQuit > 0 {
		deadline = time.After(*flagQuit)
	}
	n := 0
	var lostAt time.Time
	for {
		var sc *wm.SecureConn
		select {
		case sc = <-sessions:
		case <-deadline:
			return nil
		}
		n++
		if !lostAt.IsZero() {
			lg.Result("reconnect.session_ready_after_loss_ms=%d (Controller side)", time.Since(lostAt).Milliseconds())
		}
		err := runSession(sc, n, lines, deadline)
		if errors.Is(err, errQuit) {
			return nil
		}
		lostAt = time.Now()
		lg.Printf("session %d ended: %v. Waiting for the Agent to reconnect (no new code needed for 24 h).", n, err)
	}
}

// handshake runs CPace for a new pairing, or the HMAC resume for a known one.
func handshake(c net.Conn) (*wm.SecureConn, error) {
	c.SetDeadline(time.Now().Add(120 * time.Second))
	var r map[string]string
	if err := wm.ReadJSON(c, &r); err != nil {
		return nil, err
	}
	switch r["t"] {
	case "pair":
		ps.mu.Lock()
		secret := ps.secret
		ps.mu.Unlock()
		msgA, _ := wm.UnB64(r["a"])
		ci := cpace.NewContextInfo("waymark-agent", "waymark-controller", []byte("waymark-spike-v1 flow-a"))
		msgB, key, err := cpace.Exchange(strconv.FormatUint(uint64(secret), 10), ci, msgA)
		if err != nil {
			return nil, err
		}
		wm.WriteJSON(c, map[string]string{"t": "pair2", "b": wm.B64(msgB), "mac": wm.B64(wm.MAC(key, []byte("confirm-C")))})
		var r3 map[string]string
		if err := wm.ReadJSON(c, &r3); err != nil {
			return nil, err
		}
		mac, _ := wm.UnB64(r3["mac"])
		if r3["t"] != "pair3" || !equal(mac, wm.MAC(key, []byte("confirm-A"))) {
			ps.mu.Lock()
			ps.fails++
			f := ps.fails
			if f >= 3 {
				ps.secret, ps.fails = newSecret(), 0
			}
			ps.mu.Unlock()
			if f >= 3 {
				lg.Printf("3 failed attempts: new code")
				printCodes()
			}
			return nil, fmt.Errorf("wrong code (attempt %d of 3)", f)
		}
		sas := wm.SAS(key)
		lg.Say("\n   Αριθμός επιβεβαίωσης / Check number:  %s\n   Περιμένω επιβεβαίωση στο Agent… / Waiting for the Agent to confirm…\n", sas)
		sc := wm.NewSecureConn(c, wm.Derive(key, "session", nil), false)
		b, err := sc.Recv()
		if err != nil {
			return nil, fmt.Errorf("agent did not confirm: %w", err)
		}
		var m wm.Msg
		json.Unmarshal(b, &m)
		if m.T != "confirm" {
			return nil, errors.New("agent rejected the check number")
		}
		ps.mu.Lock()
		ps.id, ps.key, ps.until = m.Value, key, time.Now().Add(24*time.Hour)
		ps.secret = newSecret() // a code is good for one pairing
		ps.mu.Unlock()
		c.SetDeadline(time.Time{})
		lg.Printf("paired with %s, id %s", c.RemoteAddr(), m.Value)
		return sc, nil
	case "resume":
		ps.mu.Lock()
		id, key, until := ps.id, ps.key, ps.until
		ps.mu.Unlock()
		if key == nil || r["id"] != id || time.Now().After(until) {
			wm.WriteJSON(c, map[string]string{"t": "error", "err": "unknown or expired pairing"})
			return nil, errors.New("resume with unknown or expired pairing")
		}
		nA, _ := wm.UnB64(r["n"])
		nB := wm.RandBytes(16)
		wm.WriteJSON(c, map[string]string{"t": "resume2", "n": wm.B64(nB), "mac": wm.B64(wm.MAC(key, []byte("resume-C"), nA, nB))})
		var r3 map[string]string
		if err := wm.ReadJSON(c, &r3); err != nil {
			return nil, err
		}
		mac, _ := wm.UnB64(r3["mac"])
		if !equal(mac, wm.MAC(key, []byte("resume-A"), nA, nB)) {
			return nil, errors.New("resume: agent failed authentication")
		}
		c.SetDeadline(time.Time{})
		lg.Printf("agent %s resumed", c.RemoteAddr())
		return wm.NewSecureConn(c, wm.Derive(key, "session", append(nA, nB...)), false), nil
	default:
		return nil, fmt.Errorf("unexpected %q", r["t"])
	}
}

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

var errQuit = errors.New("test ended by operator")

type session struct {
	mu       sync.Mutex
	active   wm.Transport
	seq      atomic.Uint64
	pending  map[uint64]chan wm.Msg
	lastSeen atomic.Int64
}

func (s *session) deliver(m wm.Msg) {
	s.lastSeen.Store(time.Now().UnixNano())
	if m.T != "res" {
		return
	}
	s.mu.Lock()
	ch := s.pending[m.Seq]
	delete(s.pending, m.Seq)
	s.mu.Unlock()
	if ch != nil {
		ch <- m
	}
}

func (s *session) rpc(cmd string, hb bool) (wm.Msg, time.Duration, error) {
	seq := s.seq.Add(1)
	ch := make(chan wm.Msg, 1)
	s.mu.Lock()
	s.pending[seq] = ch
	tr := s.active
	s.mu.Unlock()
	t0 := time.Now()
	if err := tr.Send(wm.Msg{T: "cmd", V: wm.ProtocolVersion, ID: wm.NewID(), Seq: seq, Cmd: cmd, HB: hb}); err != nil {
		return wm.Msg{}, 0, err
	}
	select {
	case m := <-ch:
		return m, time.Since(t0), nil
	case <-time.After(2 * time.Second):
		s.mu.Lock()
		delete(s.pending, seq)
		s.mu.Unlock()
		return wm.Msg{}, 0, errors.New("timeout")
	}
}

func runSession(sc *wm.SecureConn, n int, lines <-chan string, deadline <-chan time.Time) error {
	defer sc.Close()
	t0 := time.Now()
	s := &session{pending: map[uint64]chan wm.Msg{}, active: wm.TCPTransport{SC: sc}}
	s.lastSeen.Store(time.Now().UnixNano())

	pc, dc, err := wm.NewPC(settings())
	if err != nil {
		return err
	}
	defer pc.Close()
	pc.OnICEConnectionStateChange(func(st webrtc.ICEConnectionState) { lg.Printf("ICE %s", st) })
	dcOpen := make(chan struct{})
	dc.OnOpen(func() { close(dcOpen) })
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		var m wm.Msg
		if json.Unmarshal(msg.Data, &m) == nil {
			s.deliver(m)
		}
	})

	transport := make(chan string, 1)
	errc := make(chan error, 2)
	go func() {
		for {
			b, err := sc.Recv()
			if err != nil {
				errc <- fmt.Errorf("tcp: %w", err)
				return
			}
			var m wm.Msg
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			switch m.T {
			case "sdp":
				lg.Printf("remote candidates: %s", wm.CandidateSummary(m.SDP))
				if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: m.SDP}); err != nil {
					errc <- err
					return
				}
				ans, err := pc.CreateAnswer(nil)
				if err != nil {
					errc <- err
					return
				}
				sdp, err := wm.SetLocalAndGather(pc, ans)
				if err != nil {
					errc <- err
					return
				}
				lg.Printf("local candidates: %s", wm.CandidateSummary(sdp))
				sc.Send(wm.Msg{T: "sdp", V: 1, SDP: sdp})
			case "transport":
				transport <- m.Value
			default:
				s.deliver(m)
			}
		}
	}()

	select {
	case v := <-transport:
		if v == "webrtc" {
			select {
			case <-dcOpen:
			case <-time.After(5 * time.Second):
				return errors.New("agent reports WebRTC open but it never opened here")
			}
			s.mu.Lock()
			s.active = wm.DCTransport{DC: dc}
			s.mu.Unlock()
		}
		lg.Result("session%d.ready_ms=%d transport=%s", n, time.Since(t0).Milliseconds(), v)
	case err := <-errc:
		return err
	case <-time.After(20 * time.Second):
		return errors.New("no transport decision from the agent in 20 s")
	}

	if !*flagAuto {
		lg.Say("\nΣυνδέθηκε. Πλήκτρα / Connected. Keys:\n  Enter = επόμενη / next   p = προηγούμενη / prev   b = μαύρη / black\n  m = μέτρηση / measure   q = τέλος / quit\n\n")
	}
	measureDone := make(chan struct{}, 1)
	if n == 1 {
		go func() { measure(s); measureDone <- struct{}{} }()
	}
	hb := time.NewTicker(time.Second)
	defer hb.Stop()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				lines = nil
				continue
			}
			switch line {
			case "", "n":
				cmd(s, "pres.next")
			case "p":
				cmd(s, "pres.prev")
			case "b":
				cmd(s, "pres.black")
			case "m":
				go measure(s)
			case "q":
				s.mu.Lock()
				tr := s.active
				s.mu.Unlock()
				tr.Send(wm.Msg{T: "bye", V: 1})
				time.Sleep(200 * time.Millisecond)
				return errQuit
			}
		case <-deadline:
			s.mu.Lock()
			tr := s.active
			s.mu.Unlock()
			tr.Send(wm.Msg{T: "bye", V: 1})
			time.Sleep(200 * time.Millisecond)
			return errQuit
		case <-hb.C:
			go s.rpc("ping", true)
			if time.Since(time.Unix(0, s.lastSeen.Load())) > 6*time.Second {
				return errors.New("link lost (no reply for 6 s)")
			}
		case err := <-errc:
			if s.active.Name() == "tcp" {
				return err
			}
			lg.Printf("pairing TCP closed (%v); WebRTC continues", err)
		case <-measureDone:
		}
	}
}

func cmd(s *session, c string) {
	r, rtt, err := s.rpc(c, false)
	if err != nil {
		lg.Printf("%s: %v", c, err)
		return
	}
	lg.Printf("%s -> slide %d/%d %s (%.1f ms)", c, r.State.Slide, r.State.Total, r.State.Mode, float64(rtt.Microseconds())/1000)
}

func measure(s *session) {
	s.mu.Lock()
	name := s.active.Name()
	s.mu.Unlock()
	lg.Printf("measuring over %s: %d pings, then 40 next/prev", name, *flagCount)
	var ping, pres wm.Stats
	lost := 0
	for i := 0; i < *flagCount; i++ {
		if _, d, err := s.rpc("ping", false); err == nil {
			ping.Add(d)
		} else {
			lost++
		}
		time.Sleep(50 * time.Millisecond)
	}
	for i := 0; i < 40; i++ {
		c := "pres.next"
		if i%2 == 1 {
			c = "pres.prev"
		}
		if _, d, err := s.rpc(c, false); err == nil {
			pres.Add(d)
		} else {
			lost++
		}
		time.Sleep(100 * time.Millisecond)
	}
	pass := "PASS"
	if pres.P95() > 150*time.Millisecond || lost > 0 {
		pass = "FAIL"
	}
	lg.Result("rtt.ping %s over %s", ping.String(), name)
	lg.Result("rtt.next %s lost=%d target p95<150ms %s", pres.String(), lost, pass)
}
