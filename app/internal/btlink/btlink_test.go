package btlink

import (
	"errors"
	"testing"
)

func TestParseAddr(t *testing.T) {
	a, err := ParseAddr("a4:c3:f0:12:34:56/7")
	if err != nil {
		t.Fatal(err)
	}
	if a.String() != "A4:C3:F0:12:34:56/7" || a.MAC[0] != 0xA4 || a.Channel != 7 {
		t.Fatalf("got %v", a)
	}
	for _, bad := range []string{"", "A4:C3:F0:12:34:56", "A4:C3:F0:12:34:56/0", "A4:C3:F0:12:34:56/31", "A4:C3:F0:12:34/5", "x/5"} {
		if _, err := ParseAddr(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// On a machine without an adapter (CI) Listen says so instead of failing
// in some other way; with one, the address it gives must parse back.
func TestListen(t *testing.T) {
	l, a, err := Listen()
	if errors.Is(err, ErrUnsupported) {
		t.Skip("no Bluetooth adapter")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if b, err := ParseAddr(a.String()); err != nil || b != a {
		t.Fatalf("address %v does not parse back: %v", a, err)
	}
}
