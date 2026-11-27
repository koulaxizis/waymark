package netif

import (
	"net"
	"os"
	"path/filepath"
)

func kindsByIndex() map[int]string {
	out := map[int]string{}
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		base := filepath.Join("/sys/class/net", ifc.Name)
		switch {
		case exists(filepath.Join(base, "wireless")) || exists(filepath.Join(base, "phy80211")):
			out[ifc.Index] = WiFi
		case guess(ifc.Name) == Cell:
			out[ifc.Index] = Cell
		case guess(ifc.Name) == Bluetooth || exists(filepath.Join(base, "bridge")) && hasBnep(base):
			out[ifc.Index] = Bluetooth // bnep0, or a pan0 bridge over a bnep link
		case !exists(filepath.Join(base, "device")):
			out[ifc.Index] = Other // bridges, docker, veth, VPN tunnels
		default:
			if d, err := os.Readlink(filepath.Join(base, "device", "driver")); err == nil && (filepath.Base(d) == "rndis_host" || filepath.Base(d) == "cdc_ether" || filepath.Base(d) == "cdc_ncm") {
				out[ifc.Index] = Cell // a phone sharing its mobile data over USB
			} else {
				out[ifc.Index] = Wired
			}
		}
	}
	return out
}

// hasBnep reports whether a bridge holds a Bluetooth network link.
func hasBnep(base string) bool {
	m, _ := filepath.Glob(filepath.Join(base, "brif", "bnep*"))
	return len(m) > 0
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
