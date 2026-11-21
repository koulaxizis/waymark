package netif

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// IANA ifType values the adapter table reports.
const (
	ifEthernet = 6
	ifWiFi     = 71
	ifWWAN     = 243
	ifWWAN2    = 244
)

func kindsByIndex() map[int]string {
	out := map[int]string{}
	size := uint32(16 << 10)
	for i := 0; i < 3; i++ {
		buf := make([]byte, size)
		aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_INET, windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER, 0, aa, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			return out
		}
		for a := aa; a != nil; a = a.Next {
			name := windows.UTF16PtrToString(a.FriendlyName) + " " + windows.UTF16PtrToString(a.Description)
			var k string
			switch a.IfType {
			case ifWiFi:
				k = WiFi
			case ifWWAN, ifWWAN2:
				k = Cell
			case ifEthernet:
				// Hyper-V, VPN and phone tethering adapters also say Ethernet.
				if g := guess(name); g == Other || g == Cell {
					k = g
				} else if lower(name, "remote ndis") || lower(name, "rndis") {
					k = Cell
				} else {
					k = Wired
				}
			default:
				k = guess(name)
			}
			out[int(a.IfIndex)] = k
		}
		return out
	}
	return out
}

func lower(s, sub string) bool {
	return len(s) >= len(sub) && containsFold(s, sub)
}

func containsFold(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if equalFold(s[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
