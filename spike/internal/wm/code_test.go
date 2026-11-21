package wm

import (
	"net"
	"testing"
)

func TestPairCodeRoundTrip(t *testing.T) {
	c := PairCode{IP: net.IPv4(192, 168, 7, 42), Port: 61234, Secret: 0x2abcdef1 & (1<<30 - 1)}
	short, err := c.Short()
	if err != nil || len(short) != 12 {
		t.Fatalf("short %q %v", short, err)
	}
	p, err := ParsePairCode(Group(short))
	if err != nil || !p.Short || p.Low16 != 7<<8|42 || p.Port != c.Port || p.Secret != c.Secret {
		t.Fatalf("short parse %+v %v", p, err)
	}
	l, err := ParsePairCode(c.Long())
	if err != nil || !l.FullIP.Equal(c.IP) || l.Port != c.Port || l.Secret != c.Secret {
		t.Fatalf("long parse %+v %v", l, err)
	}
	// Crockford look-alikes are accepted.
	if _, _, err := DecodeCode("oOiIlL"); err != nil {
		t.Fatal(err)
	}
}

func TestReturnCode(t *testing.T) {
	tok := RandBytes(16)
	code := ReturnCode(tok, net.IPv4(10, 0, 0, 5), 50000)
	ip, port, err := ParseReturnCode(tok, code)
	if err != nil || !ip.Equal(net.IPv4(10, 0, 0, 5)) || port != 50000 {
		t.Fatalf("%v %v %v", ip, port, err)
	}
	if _, _, err := ParseReturnCode(RandBytes(16), code); err == nil {
		t.Log("check collided by chance (1 in 4096)")
	}
}
