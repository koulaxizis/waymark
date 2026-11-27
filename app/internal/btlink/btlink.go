// Package btlink is a Bluetooth link between two computers (Proposal 14,
// point 2): an RFCOMM stream socket, the serial-port channel every Windows
// and Linux Bluetooth stack has, with no driver and no admin rights. The
// computers must have been paired once from the system's Bluetooth
// settings. The pairing's encrypted channel runs on top unchanged.
//
// An address is "AA:BB:CC:DD:EE:FF/5": the Controller's adapter and the
// RFCOMM channel it listens on. The Controller sends it with its network
// addresses, so neither side needs a service lookup (SDP).
package btlink

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// ErrUnsupported is returned where there is no Bluetooth adapter or no
// RFCOMM support (macOS for now).
var ErrUnsupported = errors.New("no Bluetooth link on this computer")

// Network is the net.Addr network of a Bluetooth link.
const Network = "bluetooth"

// Addr is one end of a link.
type Addr struct {
	MAC     [6]byte // most significant byte first, as written
	Channel int
}

func (Addr) Network() string { return Network }
func (a Addr) String() string {
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X/%d", a.MAC[0], a.MAC[1], a.MAC[2], a.MAC[3], a.MAC[4], a.MAC[5], a.Channel)
}

// ParseAddr reads "AA:BB:CC:DD:EE:FF/5".
func ParseAddr(s string) (Addr, error) {
	var a Addr
	mac, ch, ok := strings.Cut(s, "/")
	if !ok {
		return a, fmt.Errorf("bad Bluetooth address %q", s)
	}
	n, err := strconv.Atoi(ch)
	if err != nil || n < 1 || n > 30 {
		return a, fmt.Errorf("bad RFCOMM channel in %q", s)
	}
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 {
		return a, fmt.Errorf("bad Bluetooth address %q", s)
	}
	copy(a.MAC[:], hw)
	a.Channel = n
	return a, nil
}

// Listen opens an RFCOMM channel on the first adapter and returns its
// address for the peers.
func Listen() (net.Listener, Addr, error) { return listen() }

// Dial connects to a Controller's address within timeout.
func Dial(addr string, timeout time.Duration) (net.Conn, error) {
	a, err := ParseAddr(addr)
	if err != nil {
		return nil, err
	}
	return dial(a, timeout)
}

// timeoutError is the net.Error of a passed deadline.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
