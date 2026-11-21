package netif

import (
	"bufio"
	"bytes"
	"net"
	"os/exec"
	"strings"
	"sync"
)

var (
	portsOnce sync.Once
	ports     map[string]string // device -> kind
)

// hardwarePorts reads "Hardware Port: Wi-Fi / Device: en0" pairs once.
func hardwarePorts() map[string]string {
	portsOnce.Do(func() {
		ports = map[string]string{}
		b, err := exec.Command("networksetup", "-listallhardwareports").Output()
		if err != nil {
			return
		}
		var port string
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			l := sc.Text()
			switch {
			case strings.HasPrefix(l, "Hardware Port: "):
				port = strings.TrimPrefix(l, "Hardware Port: ")
			case strings.HasPrefix(l, "Device: ") && port != "":
				dev := strings.TrimPrefix(l, "Device: ")
				p := strings.ToLower(port)
				switch {
				case p == "wi-fi" || p == "airport":
					ports[dev] = WiFi
				case strings.Contains(p, "iphone") || strings.Contains(p, "bluetooth pan") || strings.Contains(p, "modem"):
					ports[dev] = Cell
				case strings.Contains(p, "ethernet") || strings.Contains(p, "lan") || strings.Contains(p, "usb 10/100"):
					ports[dev] = Wired
				case strings.Contains(p, "thunderbolt bridge"):
					ports[dev] = Other
				}
				port = ""
			}
		}
	})
	return ports
}

func kindsByIndex() map[int]string {
	out := map[int]string{}
	hp := hardwarePorts()
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if k, ok := hp[ifc.Name]; ok {
			out[ifc.Index] = k
		} else if strings.HasPrefix(ifc.Name, "en") {
			out[ifc.Index] = Other // not a hardware port: a virtual adapter
		}
	}
	return out
}
