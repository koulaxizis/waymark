// Package netif lists the IPv4 addresses of this machine with the kind of
// network each one is on, so a connection can prefer a cable over Wi-Fi and
// Wi-Fi over a phone's mobile data (Proposal 13 of the Doc).
package netif

import (
	"net"
	"sort"
	"strings"
)

// Kinds, best first.
const (
	Wired = "wired"
	WiFi  = "wifi"
	Cell  = "cell"  // mobile data: a USB or Bluetooth phone, a 4G/5G modem
	Other = "other" // virtual adapters, VPNs, unknown
	Relay = "relay" // not an interface: the relay over the internet, last resort
)

// Rank orders kinds: lower is better.
func Rank(kind string) int {
	switch kind {
	case Wired:
		return 0
	case WiFi:
		return 1
	case Cell:
		return 2
	case Relay:
		return 4
	}
	return 3
}

// Iface is one IPv4 address of an interface that is up.
type Iface struct {
	Name string
	IP   net.IP
	Kind string
}

// List returns the non-loopback IPv4 addresses, best kind first. Link-local
// addresses (169.254.x.x) come last within their kind.
func List() []Iface {
	ifs, _ := net.Interfaces()
	kinds := kindsByIndex()
	var out []Iface
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		k, ok := kinds[ifc.Index]
		if !ok {
			k = guess(ifc.Name)
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if ip4 := ipn.IP.To4(); ip4 != nil {
					out = append(out, Iface{Name: ifc.Name, IP: ip4, Kind: k})
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := Rank(out[i].Kind), Rank(out[j].Kind)
		if ri != rj {
			return ri < rj
		}
		return !out[i].IP.IsLinkLocalUnicast() && out[j].IP.IsLinkLocalUnicast()
	})
	return out
}

// KindOf returns the kind of the interface that holds ip, or Other.
func KindOf(ip net.IP) string {
	for _, f := range List() {
		if f.IP.Equal(ip) {
			return f.Kind
		}
	}
	return Other
}

// guess names the kind from the interface name alone (the fallback when the
// system cannot say).
func guess(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "wi-fi"), strings.Contains(n, "wifi"), strings.Contains(n, "wlan"),
		strings.Contains(n, "wireless"), strings.HasPrefix(n, "wl"):
		return WiFi
	case strings.HasPrefix(n, "wwan"), strings.HasPrefix(n, "ppp"), strings.HasPrefix(n, "rmnet"),
		strings.Contains(n, "cellular"), strings.Contains(n, "mobile"), strings.HasPrefix(n, "usb"):
		return Cell
	case strings.HasPrefix(n, "docker"), strings.HasPrefix(n, "veth"), strings.HasPrefix(n, "br-"),
		strings.HasPrefix(n, "virbr"), strings.HasPrefix(n, "vmnet"), strings.HasPrefix(n, "tun"),
		strings.HasPrefix(n, "tap"), strings.HasPrefix(n, "utun"), strings.Contains(n, "vethernet"),
		strings.Contains(n, "virtual"), strings.Contains(n, "vpn"):
		return Other
	case strings.Contains(n, "ethernet"), strings.HasPrefix(n, "eth"), strings.HasPrefix(n, "en"):
		return Wired
	}
	return Other
}
