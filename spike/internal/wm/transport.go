package wm

import (
	"encoding/json"

	"github.com/pion/webrtc/v4"
)

// Transport is where commands travel once a session is up: the WebRTC data
// channel, or the encrypted TCP pairing channel as a fallback.
type Transport interface {
	Send(Msg) error
	Name() string
}

// TCPTransport sends over the SecureConn.
type TCPTransport struct{ SC *SecureConn }

func (t TCPTransport) Send(m Msg) error { return t.SC.Send(m) }
func (t TCPTransport) Name() string     { return "tcp" }

// DCTransport sends over the data channel.
type DCTransport struct{ DC *webrtc.DataChannel }

func (t DCTransport) Send(m Msg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return t.DC.SendText(string(b))
}
func (t DCTransport) Name() string { return "webrtc" }

// ProtocolVersion is carried in every message as v.
const ProtocolVersion = 1

// SpikeVersion is printed in logs so results can be matched to a build.
const SpikeVersion = "spike-0.0.1"
