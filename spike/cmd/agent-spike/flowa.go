package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"filippo.io/cpace"
	"github.com/koulaxizis/waymark/spike/internal/wm"
	"github.com/pion/webrtc/v4"
)

// pairing holds what survives a dropped network: the address and the key from
// the PAKE, valid for 24 hours.
type pairing struct {
	addr  string
	id    string
	key   []byte
	until time.Time
}

func cpaceContext() *cpace.ContextInfo {
	return cpace.NewContextInfo("waymark-agent", "waymark-controller", []byte("waymark-spike-v1 flow-a"))
}

// runFlowA: the Agent dials out to the Controller (outbound only), so the
// presenter PC needs no inbound firewall rule.
func runFlowA() error {
	code := *flagCode
	for {
		if code == "" {
			code = wm.Ask("Κωδικός Controller / Controller code: ")
		}
		p, err := wm.ParsePairCode(code)
		if err != nil {
			lg.Printf("code rejected: %v", err)
			code = ""
			if *flagCode != "" {
				return err
			}
			continue
		}
		pr, sc, err := pairWithCode(p)
		if err != nil {
			lg.Printf("pairing failed: %v", err)
			code = ""
			if *flagCode != "" {
				return err
			}
			continue
		}
		return sessionLoop(pr, sc)
	}
}

func dialAny(addrs []string, timeout time.Duration) (net.Conn, string, error) {
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
		if r.e == nil && win == nil {
			rr := r
			win = &rr
			continue
		}
		if r.e == nil {
			r.c.Close()
		} else if first == nil {
			first = r.e
		}
	}
	if win != nil {
		return win.c, win.a, nil
	}
	if first == nil {
		first = errors.New("no address to try")
	}
	return nil, "", first
}

func pairWithCode(p wm.ParsedCode) (*pairing, *wm.SecureConn, error) {
	t0 := time.Now()
	cands := p.Candidates()
	lg.Printf("flow A: trying %v", cands)
	conn, addr, err := dialAny(cands, 3*time.Second)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot reach the Controller: %w", err)
	}
	tTCP := time.Since(t0)
	lg.Printf("TCP connected to %s in %s", addr, wm.Secs(tTCP))

	pw := strconv.FormatUint(uint64(p.Secret), 10)
	msgA, st, err := cpace.Start(pw, cpaceContext())
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := wm.WriteJSON(conn, map[string]string{"t": "pair", "a": wm.B64(msgA)}); err != nil {
		conn.Close()
		return nil, nil, err
	}
	var r map[string]string
	if err := wm.ReadJSON(conn, &r); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("no PAKE reply (wrong code?): %w", err)
	}
	if r["t"] != "pair2" {
		conn.Close()
		return nil, nil, fmt.Errorf("controller said: %s", r["err"])
	}
	msgB, _ := wm.UnB64(r["b"])
	key, err := st.Finish(msgB)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	macB, _ := wm.UnB64(r["mac"])
	if !hmacEqual(macB, wm.MAC(key, []byte("confirm-C"))) {
		conn.Close()
		return nil, nil, errors.New("key confirmation failed: wrong code")
	}
	wm.WriteJSON(conn, map[string]string{"t": "pair3", "mac": wm.B64(wm.MAC(key, []byte("confirm-A")))})
	tPAKE := time.Since(t0)
	sas := wm.SAS(key)
	lg.Result("flowA.pair_ms_to_sas=%d (tcp %s)", tPAKE.Milliseconds(), wm.Secs(tTCP))

	conn.SetDeadline(time.Time{})
	lg.Say("\n   Αριθμός επιβεβαίωσης / Check number:  %s\n", sas)
	lg.Say("   Πρέπει να είναι ίδιος με την οθόνη του Controller. / Must match the Controller screen.\n")
	if !*flagYes {
		ans := wm.Ask("   Ίδιος; / Same? [ν/n = ναι, οτιδήποτε άλλο = όχι]: ")
		if ans != "ν" && ans != "Ν" && ans != "n" && ans != "N" && ans != "y" && ans != "Y" && ans != "ναι" {
			wm.WriteJSON(conn, map[string]string{"t": "reject"})
			conn.Close()
			return nil, nil, errors.New("check number rejected by user")
		}
	}
	id := wm.B64(wm.MAC(key, []byte("pair-id"))[:9])
	pr := &pairing{addr: addr, id: id, key: key, until: time.Now().Add(24 * time.Hour)}
	sc := wm.NewSecureConn(conn, wm.Derive(key, "session", nil), true)
	if err := sc.Send(wm.Msg{T: "confirm", V: 1, Value: id}); err != nil {
		return nil, nil, err
	}
	lg.Printf("paired, id %s", id)
	return pr, sc, nil
}

func hmacEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// resume re-authenticates with the stored key; no code needed.
func resume(pr *pairing) (*wm.SecureConn, error) {
	conn, err := net.DialTimeout("tcp4", pr.addr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	nA := wm.RandBytes(16)
	wm.WriteJSON(conn, map[string]string{"t": "resume", "id": pr.id, "n": wm.B64(nA)})
	var r map[string]string
	if err := wm.ReadJSON(conn, &r); err != nil || r["t"] != "resume2" {
		conn.Close()
		return nil, fmt.Errorf("resume refused: %v %s", err, r["err"])
	}
	nB, _ := wm.UnB64(r["n"])
	mac, _ := wm.UnB64(r["mac"])
	if !hmacEqual(mac, wm.MAC(pr.key, []byte("resume-C"), nA, nB)) {
		conn.Close()
		return nil, errors.New("resume: controller failed authentication")
	}
	wm.WriteJSON(conn, map[string]string{"t": "resume3", "mac": wm.B64(wm.MAC(pr.key, []byte("resume-A"), nA, nB))})
	conn.SetDeadline(time.Time{})
	return wm.NewSecureConn(conn, wm.Derive(pr.key, "session", append(nA, nB...)), true), nil
}

// sessionLoop runs sessions and reconnects every 2 s after a loss (S11).
func sessionLoop(pr *pairing, sc *wm.SecureConn) error {
	pres := wm.NewFakePres()
	n := 0
	for {
		n++
		t0 := time.Now()
		err := runSession(sc, pres, n)
		lg.Printf("session %d ended after %s: %v", n, wm.Secs(time.Since(t0)), err)
		if errors.Is(err, errQuit) {
			return nil
		}
		lost := time.Now()
		attempts := 0
		var lastFail time.Time
		for {
			if time.Now().After(pr.until) {
				return errors.New("pairing expired (24 h), a new code is needed")
			}
			attempts++
			start := time.Now()
			s, err := resume(pr)
			if err == nil {
				sc = s
				// The network came back somewhere between the last failed
				// attempt and this one; that bounds the user-visible delay.
				lg.Result("reconnect.after_loss_ms=%d attempts=%d last_failed_attempt_ms_ago=%d",
					time.Since(lost).Milliseconds(), attempts, time.Since(lastFail).Milliseconds())
				break
			}
			lastFail = start
			if attempts == 1 || attempts%5 == 0 {
				lg.Printf("reconnect attempt %d: %v", attempts, err)
			}
			time.Sleep(time.Until(start.Add(2 * time.Second)))
		}
	}
}

var errQuit = errors.New("controller ended the test")

// runSession sets up WebRTC over the paired channel and serves commands until
// the link is lost.
func runSession(sc *wm.SecureConn, pres *wm.FakePres, n int) error {
	defer sc.Close()
	t0 := time.Now()
	pc, dc, err := wm.NewPC(wm.BaseSettings())
	if err != nil {
		return err
	}
	defer pc.Close()

	var mu sync.Mutex
	var active wm.Transport = wm.TCPTransport{SC: sc}
	transportChosen := false
	lastSeen := time.Now()
	incoming := make(chan struct {
		m  wm.Msg
		tr wm.Transport
	}, 16)
	errc := make(chan error, 4)

	dcOpen := make(chan struct{})
	dc.OnOpen(func() { close(dcOpen) })
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		var m wm.Msg
		if json.Unmarshal(msg.Data, &m) == nil {
			incoming <- struct {
				m  wm.Msg
				tr wm.Transport
			}{m, wm.DCTransport{DC: dc}}
		}
	})
	pc.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		lg.Printf("ICE %s", s)
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	sdp, err := wm.SetLocalAndGather(pc, offer)
	if err != nil {
		return err
	}
	lg.Printf("local candidates: %s", wm.CandidateSummary(sdp))
	if err := sc.Send(wm.Msg{T: "sdp", V: 1, SDP: sdp}); err != nil {
		return err
	}

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
			if m.T == "sdp" {
				lg.Printf("remote candidates: %s", wm.CandidateSummary(m.SDP))
				if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP}); err != nil {
					errc <- err
					return
				}
				continue
			}
			incoming <- struct {
				m  wm.Msg
				tr wm.Transport
			}{m, wm.TCPTransport{SC: sc}}
		}
	}()

	fallback := time.After(10 * time.Second)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-dcOpen:
			dcOpen = nil
			mu.Lock()
			if !transportChosen {
				transportChosen = true
				active = wm.DCTransport{DC: dc}
				sc.Send(wm.Msg{T: "transport", V: 1, Value: "webrtc"})
				lg.Result("session%d.webrtc_open_ms=%d", n, time.Since(t0).Milliseconds())
			}
			mu.Unlock()
		case <-fallback:
			mu.Lock()
			if !transportChosen {
				transportChosen = true
				sc.Send(wm.Msg{T: "transport", V: 1, Value: "tcp"})
				lg.Result("session%d.webrtc_failed_fallback_tcp after 10 s", n)
			}
			mu.Unlock()
		case in := <-incoming:
			lastSeen = time.Now()
			m := in.m
			switch m.T {
			case "cmd":
				r := pres.Handle(m)
				if !m.HB && m.Cmd != "ping" {
					lg.Printf("cmd %s via %s -> slide %d/%d", m.Cmd, in.tr.Name(), r.State.Slide, r.State.Total)
				}
				in.tr.Send(r)
			case "bye":
				return errQuit
			}
		case err := <-errc:
			return err
		case <-tick.C:
			if time.Since(lastSeen) > 6*time.Second {
				mu.Lock()
				name := active.Name()
				mu.Unlock()
				return fmt.Errorf("link lost (no message for 6 s, transport %s)", name)
			}
		}
	}
}
