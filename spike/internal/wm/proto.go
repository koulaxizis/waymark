package wm

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Msg is the one JSON shape used on every transport after pairing.
// t: "sdp", "transport", "cmd", "res", "hello", "hello-ack".
type Msg struct {
	T     string          `json:"t"`
	V     int             `json:"v,omitempty"`
	ID    string          `json:"id,omitempty"`
	Seq   uint64          `json:"seq,omitempty"`
	Cmd   string          `json:"cmd,omitempty"`
	HB    bool            `json:"hb,omitempty"`
	OK    bool            `json:"ok,omitempty"`
	Err   string          `json:"err,omitempty"`
	SDP   string          `json:"sdp,omitempty"`
	Value string          `json:"value,omitempty"`
	State *PresState      `json:"state,omitempty"`
	Extra json.RawMessage `json:"extra,omitempty"`
	FP    string          `json:"fp,omitempty"`
	MAC   string          `json:"mac,omitempty"`
	SAS   string          `json:"sas,omitempty"`
	Info  string          `json:"info,omitempty"`
}

// PresState is the fake presentation the Agent pretends to drive.
type PresState struct {
	App   string `json:"app"`
	File  string `json:"file"`
	Slide int    `json:"slide"`
	Total int    `json:"total"`
	Mode  string `json:"mode"`
}

// FakePres answers pres.* commands without touching any real application.
type FakePres struct{ S PresState }

func NewFakePres() *FakePres {
	return &FakePres{S: PresState{App: "fake", File: "spike-demo.pptx", Slide: 1, Total: 20, Mode: "show"}}
}

// Handle runs one command and returns the reply.
func (p *FakePres) Handle(m Msg) Msg {
	r := Msg{T: "res", V: 1, ID: m.ID, Seq: m.Seq, Cmd: m.Cmd, OK: true}
	switch m.Cmd {
	case "ping":
	case "state.get":
	case "pres.next":
		if p.S.Slide < p.S.Total {
			p.S.Slide++
		}
	case "pres.prev":
		if p.S.Slide > 1 {
			p.S.Slide--
		}
	case "pres.black":
		if p.S.Mode == "black" {
			p.S.Mode = "show"
		} else {
			p.S.Mode = "black"
		}
	default:
		r.OK = false
		r.Err = "UNSUPPORTED"
	}
	st := p.S
	r.State = &st
	return r
}

// Stats summarises round-trip times.
type Stats struct{ D []time.Duration }

func (s *Stats) Add(d time.Duration) { s.D = append(s.D, d) }

func (s *Stats) pct(p float64) time.Duration {
	if len(s.D) == 0 {
		return 0
	}
	c := append([]time.Duration(nil), s.D...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	i := int(math.Ceil(p*float64(len(c)))) - 1
	if i < 0 {
		i = 0
	}
	return c[i]
}

func ms(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000) }

func (s *Stats) String() string {
	if len(s.D) == 0 {
		return "no samples"
	}
	return fmt.Sprintf("n=%d p50=%s p95=%s max=%s", len(s.D), ms(s.pct(0.5)), ms(s.pct(0.95)), ms(s.pct(1)))
}

// P95 is exposed for pass/fail checks.
func (s *Stats) P95() time.Duration { return s.pct(0.95) }

// Secs formats a duration in seconds for the log.
func Secs(d time.Duration) string { return fmt.Sprintf("%.2f s", d.Seconds()) }

// Fingerprint formats a SHA-256 digest as AA:BB:...
func Fingerprint(b []byte) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02X", x)
	}
	return strings.Join(parts, ":")
}
