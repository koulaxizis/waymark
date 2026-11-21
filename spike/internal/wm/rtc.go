package wm

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/logging"
	"github.com/pion/webrtc/v4"
)

// BaseSettings is what every spike peer uses: IPv4 UDP host candidates only,
// no STUN/TURN servers (offline), no mDNS names, quiet Pion logs.
func BaseSettings() webrtc.SettingEngine {
	se := webrtc.SettingEngine{}
	lf := logging.NewDefaultLoggerFactory()
	lf.DefaultLogLevel = logging.LogLevelError
	se.LoggerFactory = lf
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	// Faster loss detection than the defaults (5 s / 25 s).
	se.SetICETimeouts(4*time.Second, 8*time.Second, 1*time.Second)
	return se
}

// NewPC builds a PeerConnection and the single negotiated data channel (id 0).
func NewPC(se webrtc.SettingEngine) (*webrtc.PeerConnection, *webrtc.DataChannel, error) {
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, nil, err
	}
	neg := true
	var id uint16
	dc, err := pc.CreateDataChannel("waymark", &webrtc.DataChannelInit{Negotiated: &neg, ID: &id})
	if err != nil {
		pc.Close()
		return nil, nil, err
	}
	return pc, dc, nil
}

// SetLocalAndGather sets the local description and waits for host candidates.
func SetLocalAndGather(pc *webrtc.PeerConnection, d webrtc.SessionDescription) (string, error) {
	done := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(d); err != nil {
		return "", err
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
	return pc.LocalDescription().SDP, nil
}

// SDPFingerprint extracts the sha-256 fingerprint bytes from an SDP.
func SDPFingerprint(sdp string) ([]byte, error) {
	for _, l := range strings.Split(sdp, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "a=fingerprint:sha-256 ") {
			h := strings.ReplaceAll(strings.TrimPrefix(l, "a=fingerprint:sha-256 "), ":", "")
			return hex.DecodeString(h)
		}
	}
	return nil, errors.New("no sha-256 fingerprint in SDP")
}

// RemoteCertFingerprint hashes the certificate the peer actually used in DTLS.
func RemoteCertFingerprint(pc *webrtc.PeerConnection) []byte {
	der := pc.SCTP().Transport().GetRemoteCertificate()
	if len(der) == 0 {
		return nil
	}
	s := sha256.Sum256(der)
	return s[:]
}

// CandidateSummary lists "typ ip:port" of the candidates in an SDP, for the log.
func CandidateSummary(sdp string) string {
	var out []string
	seen := map[string]bool{}
	for _, l := range strings.Split(sdp, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "a=candidate:") {
			continue
		}
		f := strings.Fields(l)
		if len(f) >= 8 {
			c := f[7] + " " + f[2] + " " + f[4] + ":" + f[5]
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	if len(out) == 0 {
		return "(none)"
	}
	return strings.Join(out, ", ")
}

// SniffConn wraps the Agent's single ICE socket in flow B. It reports the
// phone's ICE username fragment from the first STUN Binding Request that
// reaches us, because the phone's return code has no room for it.
type SniffConn struct {
	net.PacketConn
	localUfrag string
	once       sync.Once
	Found      chan SniffResult
}

// SniffResult is the phone's ufrag and source address.
type SniffResult struct {
	Ufrag string
	From  net.Addr
	At    time.Time
}

func NewSniffConn(pc net.PacketConn, localUfrag string) *SniffConn {
	return &SniffConn{PacketConn: pc, localUfrag: localUfrag, Found: make(chan SniffResult, 1)}
}

func (s *SniffConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := s.PacketConn.ReadFrom(b)
	if err == nil && n >= 20 {
		if u, ok := stunUsername(b[:n]); ok {
			if parts := strings.SplitN(u, ":", 2); len(parts) == 2 && parts[0] == s.localUfrag {
				s.once.Do(func() { s.Found <- SniffResult{Ufrag: parts[1], From: addr, At: time.Now()} })
			}
		}
	}
	return n, addr, err
}

const stunMagic = 0x2112A442

// stunUsername returns USERNAME from a STUN Binding Request.
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

// StunIndication is a STUN Binding Indication. The Agent sends it to the phone
// only to open the stateful firewall path for the phone's checks; the phone
// ignores it.
func StunIndication() []byte {
	b := make([]byte, 20)
	binary.BigEndian.PutUint16(b[0:2], 0x0011)
	binary.BigEndian.PutUint32(b[4:8], stunMagic)
	copy(b[8:], RandBytes(12))
	return b
}
