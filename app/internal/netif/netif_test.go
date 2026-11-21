package netif

import "testing"

func TestGuess(t *testing.T) {
	for name, want := range map[string]string{
		"Wi-Fi": WiFi, "wlan0": WiFi, "wlp3s0": WiFi, "Ethernet 2": Wired, "eth0": Wired, "enp0s31f6": Wired,
		"docker0": Other, "vEthernet (WSL)": Other, "wwan0": Cell, "usb0": Cell, "utun3": Other,
	} {
		if got := guess(name); got != want {
			t.Errorf("guess(%q) = %s, want %s", name, got, want)
		}
	}
}

func TestListSorted(t *testing.T) {
	l := List()
	for i := 1; i < len(l); i++ {
		if Rank(l[i-1].Kind) > Rank(l[i].Kind) {
			t.Fatalf("not sorted: %v", l)
		}
	}
	t.Log(l)
}
