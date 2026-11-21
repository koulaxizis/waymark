// Package rtc connects a browser Controller (phone, tablet, any laptop
// browser) to the Agent: flow B of the Doc.
//
// A web page cannot listen on a port, so the roles turn around. The Agent
// shows a QR with its addresses, its DTLS fingerprint and a one-time token.
// The page shows a 12-character return code with its own address. The
// presenter types it on the Agent, which then sends first to open its own
// firewall path, and the page's ICE checks come in as replies. ICE
// credentials come from the token, and the page proves it holds the token
// and binds its DTLS certificate inside the channel, so the return code only
// needs to carry an address.
package rtc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/logging"
	"github.com/pion/webrtc/v4"

	"github.com/koulaxizis/waymark/app/internal/code"
	"github.com/koulaxizis/waymark/app/internal/netif"
	"github.com/koulaxizis/waymark/app/internal/proto"
	"github.com/koulaxizis/waymark/app/internal/secure"
)

// PageURL is the browser Controller. It loads once from the site and then
// talks only to the Agent on the LAN; no command or file passes the site.
var PageURL = "https://waymarked.site/remote/"

// InviteLife is how long a QR stays valid.
const InviteLife = 10 * time.Minute

func baseSettings() webrtc.SettingEngine {
	se := webrtc.SettingEngine{}
	lf := logging.NewDefaultLoggerFactory()
	lf.DefaultLogLevel = logging.LogLevelError
	se.LoggerFactory = lf
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	// Ride out a Wi-Fi drop of up to 30 s on the same connection (the
	// browser gives up about then too); after that the page resumes.
	se.SetICETimeouts(3*time.Second, 30*time.Second, time.Second)
	return se
}

func fingerprintHex(b []byte) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02X", x)
	}
	return strings.Join(parts, ":")
}

func sdpFingerprint(sdp string) ([]byte, error) {
	for _, l := range strings.Split(sdp, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "a=fingerprint:sha-256 ") {
			return hex.DecodeString(strings.ReplaceAll(strings.TrimPrefix(l, "a=fingerprint:sha-256 "), ":", ""))
		}
	}
	return nil, errors.New("no sha-256 fingerprint in SDP")
}

// Hello is what the page sends first, inside the data channel.
type Hello struct {
	T    string     `json:"t"`
	V    int        `json:"v"`
	FP   string     `json:"fp"`  // the page's DTLS fingerprint
	MAC  string     `json:"mac"` // HMAC(token, "wm-b-hello", fp, agentFP)
	Name string     `json:"name"`
	Role proto.Role `json:"role"`
}

// Invite is one QR on the Agent screen.
type Invite struct {
	URL     string
	Expires time.Time

	token, fp []byte
	cert      webrtc.Certificate
	port      int
	udp       *net.UDPConn
	sniff     *sniffConn
	mux       *ice.UDPMuxDefault
	pc        *webrtc.PeerConnection
	dc        *webrtc.DataChannel
	used      bool
	mu        sync.Mutex
}

// NewInvite opens a UDP socket and builds the QR link.
func NewInvite(agentName string) (*Invite, error) {
	token := secure.RandBytes(16)
	udp, port, err := listenUDP(0)
	if err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		udp.Close()
		return nil, err
	}
	cert, err := webrtc.GenerateCertificate(key)
	if err != nil {
		udp.Close()
		return nil, err
	}
	inv := &Invite{token: token, udp: udp, port: port, cert: *cert, Expires: time.Now().Add(InviteLife)}
	ap, err := newAgentPC(udp, token, inv.cert)
	if err != nil {
		udp.Close()
		return nil, err
	}
	inv.sniff, inv.mux, inv.pc, inv.dc, inv.fp = ap.sniff, ap.mux, ap.pc, ap.dc, ap.fp
	var ips []string
	for _, f := range netif.List() {
		ips = append(ips, f.IP.String())
	}
	if len(ips) == 0 {
		inv.Close()
		return nil, errors.New("no network: connect this laptop to Wi-Fi or a cable")
	}
	payload := fmt.Sprintf("WM1.%s.%d.%s.%s", strings.Join(ips, ","), port, secure.B64(inv.fp), secure.B64(token))
	inv.URL = PageURL + "#" + payload
	return inv, nil
}

// agentPC is the Agent end of one browser connection: an ICE-lite offerer on
// a UDP socket, with ICE credentials derived from secret and a fixed DTLS
// certificate, so the page can rebuild the offer from what it holds.
type agentPC struct {
	sniff *sniffConn
	mux   *ice.UDPMuxDefault
	pc    *webrtc.PeerConnection
	dc    *webrtc.DataChannel
	fp    []byte
}

func newAgentPC(udp *net.UDPConn, secret []byte, cert webrtc.Certificate) (*agentPC, error) {
	ufrag := secure.B64(secure.MAC(secret, []byte("ice-ufrag")))[:8]
	pwd := secure.B64(secure.MAC(secret, []byte("ice-pwd")))[:24]
	ap := &agentPC{}
	ap.sniff = &sniffConn{PacketConn: udp, localUfrag: ufrag, found: make(chan sniffResult, 1)}
	ap.mux = ice.NewUDPMuxDefault(ice.UDPMuxParams{UDPConn: ap.sniff})
	se := baseSettings()
	se.SetLite(true)
	se.SetICECredentials(ufrag, pwd)
	se.SetICEUDPMux(ap.mux)
	// The page's certificate is checked in-channel against the secret.
	se.DisableCertificateFingerprintVerification(true)
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))
	pc, err := api.NewPeerConnection(webrtc.Configuration{Certificates: []webrtc.Certificate{cert}})
	if err != nil {
		ap.mux.Close()
		return nil, err
	}
	ap.pc = pc
	fail := func(err error) (*agentPC, error) { pc.Close(); ap.mux.Close(); return nil, err }
	neg, id := true, uint16(0)
	if ap.dc, err = pc.CreateDataChannel("waymark", &webrtc.DataChannelInit{Negotiated: &neg, ID: &id}); err != nil {
		return fail(err)
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return fail(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		return fail(err)
	}
	select {
	case <-gathered:
	case <-time.After(3 * time.Second):
	}
	if ap.fp, err = sdpFingerprint(pc.LocalDescription().SDP); err != nil {
		return fail(err)
	}
	return ap, nil
}

// answer sets the page's side from what the sniffer saw: its ICE username
// fragment and address. A lite agent never sends checks, and the page's
// certificate is checked in-channel, so the rest can be placeholders.
func (ap *agentPC) answer(sr sniffResult) error {
	ua, ok := sr.from.(*net.UDPAddr)
	if !ok {
		return errors.New("odd peer address")
	}
	sdp := strings.Join([]string{
		"v=0", "o=- 2 2 IN IP4 127.0.0.1", "s=-", "t=0 0", "a=group:BUNDLE 0",
		"m=application 9 UDP/DTLS/SCTP webrtc-datachannel", "c=IN IP4 0.0.0.0",
		"a=ice-ufrag:" + sr.ufrag,
		"a=ice-pwd:" + strings.Repeat("x", 24),
		"a=fingerprint:sha-256 " + fingerprintHex(make([]byte, 32)),
		"a=setup:active", "a=mid:0", "a=sctp-port:5000", "a=max-message-size:262144",
		fmt.Sprintf("a=candidate:1 1 udp 2130706431 %s %d typ host", ua.IP, ua.Port), "",
	}, "\r\n")
	return ap.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp})
}

func (ap *agentPC) close() {
	ap.pc.Close()
	ap.mux.Close()
}

// remoteFP is the SHA-256 of the page's DTLS certificate.
func (ap *agentPC) remoteFP() []byte {
	der := ap.pc.SCTP().Transport().GetRemoteCertificate()
	sum := sha256.Sum256(der)
	return sum[:]
}

// listenUDP binds port, or a random high port when port is 0.
func listenUDP(port int) (*net.UDPConn, int, error) {
	if port != 0 {
		var err error
		for i := 0; i < 20; i++ { // the old socket may take a moment to go
			c, e := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
			if e == nil {
				return c, port, nil
			}
			err = e
			time.Sleep(250 * time.Millisecond)
		}
		return nil, 0, err
	}
	for i := 0; i < 50; i++ {
		b := secure.RandBytes(2)
		p := 49152 + int(binary.BigEndian.Uint16(b)%16384)
		c, err := net.ListenUDP("udp4", &net.UDPAddr{Port: p})
		if err == nil {
			return c, p, nil
		}
	}
	return nil, 0, errors.New("no free UDP port")
}

// Close releases the socket if the invite was not used.
func (inv *Invite) Close() {
	inv.mu.Lock()
	used := inv.used
	inv.mu.Unlock()
	if used {
		return
	}
	if inv.pc != nil {
		inv.pc.Close()
	}
	if inv.mux != nil {
		inv.mux.Close()
	}
	if inv.udp != nil {
		inv.udp.Close()
	}
}

// Prompt asks the presenter, as in package pair.
type Prompt func(sas, peerName string, role proto.Role) (proto.Role, bool)

// Accepted is a browser Controller that the presenter accepted.
type Accepted struct {
	Link   *Link
	ID     string
	Name   string
	Role   proto.Role
	Resume *Resume // brings this page back after a lost link
}

// Accept takes the return code typed on the Agent and runs the connection.
func (inv *Invite) Accept(returnCode string, prompt Prompt) (*Accepted, error) {
	inv.mu.Lock()
	if inv.used {
		inv.mu.Unlock()
		return nil, errors.New("this QR was used; show a new one")
	}
	inv.mu.Unlock()
	if time.Now().After(inv.Expires) {
		return nil, errors.New("the QR expired; show a new one")
	}
	ip, port, err := code.ParseReturnCode(inv.token, returnCode)
	if err != nil {
		return nil, err
	}
	peer := &net.UDPAddr{IP: ip, Port: port}

	// Open the path through the Agent's firewall by sending first.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(300 * time.Millisecond)
		defer t.Stop()
		for i := 0; i < 300; i++ {
			inv.udp.WriteTo(stunIndication(), peer)
			select {
			case <-stop:
				return
			case <-t.C:
			}
		}
	}()

	var sr sniffResult
	select {
	case sr = <-inv.sniff.found:
	case <-time.After(90 * time.Second):
		return nil, errors.New("nothing arrived from the browser in 90 s: same network? press Connect on the page")
	}
	ap := &agentPC{sniff: inv.sniff, mux: inv.mux, pc: inv.pc, dc: inv.dc, fp: inv.fp}
	link := newLink(ap, inv.udp, sr.from)
	if err := ap.answer(sr); err != nil {
		return nil, fmt.Errorf("remote description: %w", err)
	}
	select {
	case <-link.opened:
	case <-time.After(20 * time.Second):
		return nil, errors.New("the channel did not open in 20 s")
	}
	link.SetReadDeadline(time.Now().Add(10 * time.Second))
	raw, err := link.Recv()
	if err != nil {
		return nil, errors.New("no hello from the browser")
	}
	var h Hello
	json.Unmarshal(raw, &h)
	pageFP, _ := secure.UnB64(h.FP)
	mac, _ := secure.UnB64(h.MAC)
	if h.T != "hello" || !secure.Equal(mac, secure.MAC(inv.token, []byte("wm-b-hello"), pageFP, inv.fp)) {
		return nil, errors.New("the browser does not hold this QR")
	}
	if h.V != proto.Version {
		link.Send(map[string]any{"t": "err", "code": proto.ErrVersion, "err": "update the page"})
		return nil, fmt.Errorf("page protocol %d", h.V)
	}
	if !secure.Equal(ap.remoteFP(), pageFP) {
		return nil, errors.New("the browser's certificate does not match its hello")
	}
	if !h.Role.Valid() {
		h.Role = proto.RoleOperator
	}
	sas := secure.SAS(inv.token, pageFP, inv.fp)
	link.Send(map[string]any{"t": "hello-ack", "mac": secure.B64(secure.MAC(inv.token, []byte("wm-b-ack"), pageFP, inv.fp))})
	name := strings.TrimSpace(h.Name)
	if name == "" {
		name = "Browser"
	}
	if len(name) > 60 {
		name = name[:60]
	}
	link.SetReadDeadline(time.Time{})
	granted, ok := prompt(sas, name, h.Role)
	if !ok {
		link.Send(map[string]any{"t": "reject"})
		time.Sleep(200 * time.Millisecond)
		link.Close()
		return nil, errors.New("the presenter did not accept the connection")
	}
	if !granted.Valid() || granted.Rank() > h.Role.Rank() {
		granted = h.Role
	}
	id := "b-" + secure.B64(secure.MAC(inv.token, []byte("pair-id"), pageFP)[:9])
	// The resume key lets this page come back without a new QR for 24 h.
	rk := secure.RandBytes(16)
	if err := link.Send(map[string]any{"t": "confirm", "id": id, "role": granted, "rk": secure.B64(rk)}); err != nil {
		link.Close()
		return nil, err
	}
	inv.mu.Lock()
	inv.used = true
	inv.mu.Unlock()
	res := &Resume{ID: id, key: rk, cert: inv.cert, fp: inv.fp, port: inv.port, Until: time.Now().Add(ResumeLife), codes: make(chan *net.UDPAddr, 1)}
	return &Accepted{Link: link, ID: id, Name: name, Role: granted, Resume: res}, nil
}

// Link is the data channel as a message pipe, shaped like secure.Conn.
type Link struct {
	ap       *agentPC
	udp      *net.UDPConn
	peer     net.Addr
	in       chan []byte
	opened   chan struct{}
	closed   chan struct{}
	once     sync.Once
	mu       sync.Mutex
	deadline time.Time
}

func newLink(ap *agentPC, udp *net.UDPConn, peer net.Addr) *Link {
	l := &Link{ap: ap, udp: udp, peer: peer, in: make(chan []byte, 256), opened: make(chan struct{}), closed: make(chan struct{})}
	var openOnce sync.Once
	ap.dc.OnOpen(func() { openOnce.Do(func() { close(l.opened) }) })
	ap.dc.OnMessage(func(m webrtc.DataChannelMessage) {
		select {
		case l.in <- m.Data:
		case <-l.closed:
		}
	})
	ap.dc.OnClose(func() { l.shut() })
	var punching sync.Once
	ap.pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		switch s {
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			l.shut()
		case webrtc.PeerConnectionStateDisconnected:
			// Keep the way through this laptop's firewall open while the
			// phone is away (a Wi-Fi drop), so its checks get in again.
			punching.Do(func() { go l.punch() })
		}
	})
	return l
}

func (l *Link) punch() {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-l.closed:
			return
		case <-t.C:
			if l.ap.pc.ConnectionState() == webrtc.PeerConnectionStateConnected {
				continue
			}
			l.udp.WriteTo(stunIndication(), l.peer)
		}
	}
}

func (l *Link) shut() { l.once.Do(func() { close(l.closed) }) }

// Done is closed when the browser connection has failed for good: the
// browser must then come back through Resume.
func (l *Link) Done() <-chan struct{} { return l.closed }

// Send writes one JSON message.
func (l *Link) Send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	select {
	case <-l.closed:
		return errors.New("link closed")
	default:
	}
	// Back-pressure: wait while the SCTP buffer is full (large downloads).
	for i := 0; l.ap.dc.BufferedAmount() > 4<<20 && i < 1500; i++ {
		select {
		case <-l.closed:
			return errors.New("link closed")
		case <-time.After(10 * time.Millisecond):
		}
	}
	return l.ap.dc.SendText(string(b))
}

// Recv returns the next message, or an error after the read deadline.
func (l *Link) Recv() ([]byte, error) {
	l.mu.Lock()
	d := l.deadline
	l.mu.Unlock()
	var timer <-chan time.Time
	if !d.IsZero() {
		t := time.NewTimer(time.Until(d))
		defer t.Stop()
		timer = t.C
	}
	select {
	case b := <-l.in:
		return b, nil
	case <-l.closed:
		return nil, errors.New("link closed")
	case <-timer:
		return nil, ErrTimeout
	}
}

// ErrTimeout is a quiet link that may still come back.
var ErrTimeout = errors.New("read timeout")

func (l *Link) SetReadDeadline(t time.Time) error {
	l.mu.Lock()
	l.deadline = t
	l.mu.Unlock()
	return nil
}

// Close ends the connection and frees the socket.
func (l *Link) Close() error {
	l.shut()
	l.ap.close()
	l.udp.Close()
	return nil
}

// sniffConn reports the page's ICE username fragment from the first STUN
// Binding Request, because the return code has no room for it.
type sniffConn struct {
	net.PacketConn
	localUfrag string
	once       sync.Once
	found      chan sniffResult
}

type sniffResult struct {
	ufrag string
	from  net.Addr
}

func (s *sniffConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := s.PacketConn.ReadFrom(b)
	if err == nil && n >= 20 {
		if u, ok := stunUsername(b[:n]); ok {
			if p := strings.SplitN(u, ":", 2); len(p) == 2 && p[0] == s.localUfrag {
				s.once.Do(func() { s.found <- sniffResult{ufrag: p[1], from: addr} })
			}
		}
	}
	return n, addr, err
}

const stunMagic = 0x2112A442

func stunUsername(b []byte) (string, bool) {
	if binary.BigEndian.Uint16(b[0:2]) != 0x0001 || binary.BigEndian.Uint32(b[4:8]) != stunMagic {
		return "", false
	}
	l := int(binary.BigEndian.Uint16(b[2:4]))
	if 20+l > len(b) {
		return "", false
	}
	p := b[20 : 20+l]
	for len(p) >= 4 {
		t := binary.BigEndian.Uint16(p[0:2])
		al := int(binary.BigEndian.Uint16(p[2:4]))
		if 4+al > len(p) {
			return "", false
		}
		if t == 0x0006 {
			return string(p[4 : 4+al]), true
		}
		pad := (4 - al%4) % 4
		if 4+al+pad > len(p) {
			break
		}
		p = p[4+al+pad:]
	}
	return "", false
}

// stunIndication opens the stateful firewall path; the page ignores it.
func stunIndication() []byte {
	b := make([]byte, 20)
	binary.BigEndian.PutUint16(b[0:2], 0x0011)
	binary.BigEndian.PutUint32(b[4:8], stunMagic)
	copy(b[8:], secure.RandBytes(12))
	return b
}
