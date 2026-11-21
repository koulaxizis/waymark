package rtc

import (
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/koulaxizis/waymark/app/internal/code"
	"github.com/koulaxizis/waymark/app/internal/proto"
	"github.com/koulaxizis/waymark/app/internal/secure"
)

// ResumeLife is how long a paired page can come back without a new QR, the
// same as a pairing of flow A.
const ResumeLife = 24 * time.Hour

// Resume lets a paired page come back after its connection failed for good
// (Wi-Fi gone for over 30 s, a new address, a reload): the page rebuilds the
// offer from what it kept and the resume key it got at pairing, and the
// Agent waits on the same UDP port with the same certificate. No QR, no
// check number, no question to the presenter: the key proves it is the page
// that was accepted (Proposal 13).
//
// When this laptop's firewall drops the page's packets (a Windows "Public"
// network), the page shows a return code after a few tries; typed on the
// Agent it opens the way as at pairing.
type Resume struct {
	ID    string
	Until time.Time

	key   []byte
	cert  webrtc.Certificate
	fp    []byte
	port  int
	codes chan *net.UDPAddr
	mu    sync.Mutex
}

// Port is the UDP port the page comes back to.
func (r *Resume) Port() int { return r.port }

// TryCode takes a return code typed on the Agent. It reports whether the
// code belongs to this page; the waiting Wait then opens the way to it.
func (r *Resume) TryCode(s string) bool {
	ip, port, err := code.ParseReturnCode(r.key, s)
	if err != nil {
		return false
	}
	select {
	case r.codes <- &net.UDPAddr{IP: ip, Port: port}:
	default:
	}
	return true
}

// Wait blocks until the page is back, the resume time is over, or stop is
// closed, and returns the new link.
func (r *Resume) Wait(stop <-chan struct{}) (*Link, error) {
	for {
		if time.Now().After(r.Until) {
			return nil, errors.New("resume time over: the page needs a new QR")
		}
		select {
		case <-stop:
			return nil, errors.New("stopped")
		default:
		}
		l, err := r.once(stop)
		if err == nil {
			return l, nil
		}
		select {
		case <-stop:
			return nil, errors.New("stopped")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// once is one wait for the page on a fresh socket and connection.
func (r *Resume) once(stop <-chan struct{}) (*Link, error) {
	udp, _, err := listenUDP(r.port)
	if err != nil {
		return nil, err
	}
	ap, err := newAgentPC(udp, r.key, r.cert)
	if err != nil {
		udp.Close()
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			ap.close()
			udp.Close()
		}
	}()
	done := make(chan struct{})
	defer close(done)
	var sr sniffResult
	for got := false; !got; {
		select {
		case <-stop:
			return nil, errors.New("stopped")
		case to := <-r.codes:
			go punchTo(udp, to, done)
		case sr = <-ap.sniff.found:
			got = true
		case <-time.After(time.Minute):
			return nil, errors.New("no page yet") // a fresh socket now and then
		}
	}
	link := newLink(ap, udp, sr.from)
	if err := ap.answer(sr); err != nil {
		return nil, err
	}
	select {
	case <-link.opened:
	case <-time.After(15 * time.Second):
		return nil, errors.New("the channel did not open")
	}
	link.SetReadDeadline(time.Now().Add(10 * time.Second))
	raw, err := link.Recv()
	if err != nil {
		return nil, errors.New("no resume hello")
	}
	var h struct {
		T   string `json:"t"`
		V   int    `json:"v"`
		ID  string `json:"id"`
		FP  string `json:"fp"`
		MAC string `json:"mac"`
	}
	json.Unmarshal(raw, &h)
	pageFP, _ := secure.UnB64(h.FP)
	mac, _ := secure.UnB64(h.MAC)
	if h.T != "resume" || h.ID != r.ID || !secure.Equal(mac, secure.MAC(r.key, []byte("wm-b-resume"), pageFP, r.fp)) {
		return nil, errors.New("not the paired page")
	}
	if !secure.Equal(ap.remoteFP(), pageFP) {
		return nil, errors.New("the page's certificate does not match its hello")
	}
	if h.V != proto.Version {
		link.Send(map[string]any{"t": "err", "code": proto.ErrVersion, "err": "update the page"})
		return nil, errors.New("page protocol differs")
	}
	if err := link.Send(map[string]any{"t": "resumed", "mac": secure.B64(secure.MAC(r.key, []byte("wm-b-resumed"), pageFP, r.fp))}); err != nil {
		return nil, err
	}
	link.SetReadDeadline(time.Time{})
	ok = true
	return link, nil
}

func punchTo(udp *net.UDPConn, to *net.UDPAddr, done <-chan struct{}) {
	t := time.NewTicker(300 * time.Millisecond)
	defer t.Stop()
	for i := 0; i < 300; i++ {
		udp.WriteTo(stunIndication(), to)
		select {
		case <-done:
			return
		case <-t.C:
		}
	}
}
