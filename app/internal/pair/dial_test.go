package pair

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/koulaxizis/waymark/app/internal/netif"
	"github.com/koulaxizis/waymark/app/internal/proto"
	"github.com/koulaxizis/waymark/app/internal/relay"
)

// listen accepts on loopback and counts the connections it gets.
func listen(t *testing.T) (string, *int32) {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	var n int32
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			atomic.AddInt32(&n, 1)
			c.Close()
		}
	}()
	return l.Addr().String(), &n
}

// closedAddr is a loopback port nobody listens on: dialling it fails at once.
func closedAddr(t *testing.T) string {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := l.Addr().String()
	l.Close()
	return a
}

func TestBluetoothOnlyWhenOthersFail(t *testing.T) {
	wifi, _ := listen(t)
	bt, btHits := listen(t)
	c, i, err := dialBest([]Path{{Addr: wifi, Kind: netif.WiFi}, {Addr: bt, Kind: netif.Bluetooth}}, nil)
	if err != nil || i != 0 {
		t.Fatalf("got path %d, %v; want Wi-Fi", i, err)
	}
	c.Close()
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(btHits) != 0 {
		t.Fatal("Bluetooth was dialled although Wi-Fi answered")
	}

	start := time.Now()
	c, i, err = dialBest([]Path{{Addr: closedAddr(t), Kind: netif.Wired}, {Addr: bt, Kind: netif.Bluetooth}}, nil)
	if err != nil || i != 1 {
		t.Fatalf("got path %d, %v; want Bluetooth once the cable failed", i, err)
	}
	c.Close()
	if d := time.Since(start); d > DialTimeout {
		t.Fatalf("Bluetooth waited %v; it should start as soon as the cable fails", d)
	}
}

func TestPathsOrder(t *testing.T) {
	p := &Pairing{port: 5000}
	p.SetAddrs(addrsWithRelay())
	var kinds []string
	for _, pt := range p.Paths() {
		kinds = append(kinds, pt.Kind)
	}
	want := []string{netif.Wired, netif.Cell, netif.Relay, netif.Bluetooth}
	if len(kinds) != len(want) {
		t.Fatalf("paths %v", kinds)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("paths %v, want %v", kinds, want)
		}
	}
}

func addrsWithRelay() proto.AddrsData {
	return proto.AddrsData{Port: 5000, Addrs: []proto.Addr{
		{IP: "192.168.1.5", Kind: netif.Wired},
		{IP: "192.168.44.2", Kind: netif.Bluetooth},
		{IP: "10.0.0.7", Kind: netif.Cell},
	}, Relay: &relay.Config{URL: "wss://relay.example", Room: "0123456789abcdef0123456789abcdef"}}
}
