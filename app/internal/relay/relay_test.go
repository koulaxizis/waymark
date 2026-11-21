package relay

import (
	"bytes"
	"io"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

const room = "0123456789abcdef0123456789abcdef"

// TestRelayJoinsTwoSides runs against Handler, or against a running Worker
// when WM_RELAY_URL and WM_RELAY_KEY are set (wrangler dev).
func TestRelayJoinsTwoSides(t *testing.T) {
	u, k := os.Getenv("WM_RELAY_URL"), os.Getenv("WM_RELAY_KEY")
	if u == "" {
		srv := httptest.NewServer(Handler("k"))
		defer srv.Close()
		u, k = srv.URL, "k"
	}
	cfg := Config{URL: CleanURL(u), Key: k, Room: room}

	if _, err := Dial(cfg, "a", time.Second); err == nil {
		// The relay closes an Agent with no Controller waiting at once.
		c, _ := Dial(cfg, "a", time.Second)
		if c != nil {
			c.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := c.Read(make([]byte, 1)); err == nil {
				t.Fatal("an Agent with nobody waiting got data")
			}
		}
	}
	bad := cfg
	bad.Key = "wrong"
	if _, err := Dial(bad, "c", time.Second); err == nil {
		t.Fatal("a wrong access key was let in")
	}

	got := make(chan []byte, 1)
	go func() {
		c, err := Wait(cfg, make(chan struct{}))
		if err != nil {
			t.Error(err)
			got <- nil
			return
		}
		b := make([]byte, 5)
		io.ReadFull(c, b)
		c.Write([]byte("pong!"))
		got <- b
	}()
	time.Sleep(200 * time.Millisecond)
	a, err := Dial(cfg, "a", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	a.Write([]byte("hello"))
	if b := <-got; !bytes.Equal(b, []byte("hello")) {
		t.Fatalf("controller got %q", b)
	}
	r := make([]byte, 5)
	a.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(a, r); err != nil || string(r) != "pong!" {
		t.Fatalf("agent got %q, %v", r, err)
	}
}

func TestCleanURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://wm.example.workers.dev/": "wss://wm.example.workers.dev",
		"wm.example.workers.dev":          "wss://wm.example.workers.dev",
		"http://10.0.0.5:8080":            "ws://10.0.0.5:8080",
		"":                                "",
		"ftp://x":                         "",
	} {
		if got := CleanURL(in); got != want {
			t.Errorf("CleanURL(%q) = %q, want %q", in, got, want)
		}
	}
}
