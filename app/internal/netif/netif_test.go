package netif

import "testing"

func TestGuess(t *testing.T) {
	for name, want := range map[string]string{
		"Wi-Fi": WiFi, "wlan0": WiFi, "wlp3s0": WiFi, "Ethernet 2": Wired, "eth0": Wired, "enp0s31f6": Wired,
		"docker0": Other, "vEthernet (WSL)": Other, "wwan0": Cell, "usb0": Cell, "utun3": Other,
		"bnep0": Bluetooth, "Bluetooth Network Connection": Bluetooth, "pan0": Bluetooth,
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

func TestBluetoothLast(t *testing.T) {
	if !(Rank(Wired) < Rank(WiFi) && Rank(WiFi) < Rank(Cell) && Rank(Cell) < Rank(Relay) && Rank(Relay) < Rank(Bluetooth)) {
		t.Fatal("order must be cable, Wi-Fi, mobile data, relay, Bluetooth")
	}
	if Rank(Other) >= Rank(Relay) {
		t.Fatal("an unknown network still comes before the relay")
	}
}
