// Package wm holds the shared pieces of the Waymark pairing spike.
// This is throwaway spike code (branch spike/pairing). It never ships.
package wm

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
)

// Crockford base32 alphabet: no I, L, O, U.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// EncodeBits writes the lowest nbits of v as Crockford base32, nbits/5 chars.
func EncodeBits(v *big.Int, nchars int) string {
	out := make([]byte, nchars)
	x := new(big.Int).Set(v)
	mask := big.NewInt(31)
	for i := nchars - 1; i >= 0; i-- {
		d := new(big.Int).And(x, mask).Int64()
		out[i] = crockford[d]
		x.Rsh(x, 5)
	}
	return string(out)
}

// DecodeCode turns user input into an integer. It ignores dashes and spaces,
// is case-insensitive and maps O->0, I/L->1 as Crockford allows.
func DecodeCode(s string) (*big.Int, int, error) {
	s = strings.ToUpper(s)
	v := new(big.Int)
	n := 0
	for _, r := range s {
		switch r {
		case '-', ' ', '\t', '.':
			continue
		case 'O':
			r = '0'
		case 'I', 'L':
			r = '1'
		}
		idx := strings.IndexRune(crockford, r)
		if idx < 0 {
			return nil, 0, fmt.Errorf("invalid character %q", r)
		}
		v.Lsh(v, 5)
		v.Or(v, big.NewInt(int64(idx)))
		n++
	}
	return v, n, nil
}

// Group formats a code as XXXX-XXXX-XXXX.
func Group(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteRune(r)
	}
	return b.String()
}

const portBase = 49152 // short codes only carry ports 49152..65535 (14 bits)

// PairCode is what the Controller shows in flow A.
type PairCode struct {
	IP     net.IP // IPv4
	Port   int
	Secret uint32 // 30 bits
}

// Short returns the 12-char code: low 16 bits of the IPv4, 14-bit port offset,
// 30-bit secret. The Agent fills in the top two octets from its own interfaces.
func (c PairCode) Short() (string, error) {
	if c.Port < portBase || c.Port > 65535 {
		return "", errors.New("port outside 49152-65535")
	}
	ip := c.IP.To4()
	v := new(big.Int).SetUint64(uint64(ip[2])<<8 | uint64(ip[3]))
	v.Lsh(v, 14)
	v.Or(v, big.NewInt(int64(c.Port-portBase)))
	v.Lsh(v, 30)
	v.Or(v, big.NewInt(int64(c.Secret&(1<<30-1))))
	return EncodeBits(v, 12), nil
}

// Long returns the 16-char code with the full IPv4 and port, for networks where
// the two machines do not share the first two octets.
func (c PairCode) Long() string {
	ip := c.IP.To4()
	v := new(big.Int).SetUint64(uint64(binary.BigEndian.Uint32(ip)))
	v.Lsh(v, 16)
	v.Or(v, big.NewInt(int64(c.Port)))
	v.Lsh(v, 30)
	v.Or(v, big.NewInt(int64(c.Secret&(1<<30-1))))
	v.Lsh(v, 2)
	return EncodeBits(v, 16)
}

// ParsedCode is the result of parsing what the user typed on the Agent.
type ParsedCode struct {
	Short  bool
	Low16  uint16 // short form only
	FullIP net.IP // long form only
	Port   int
	Secret uint32
}

// ParsePairCode accepts the 12-char or 16-char form.
func ParsePairCode(s string) (ParsedCode, error) {
	v, n, err := DecodeCode(s)
	if err != nil {
		return ParsedCode{}, err
	}
	switch n {
	case 12:
		secret := new(big.Int).And(v, big.NewInt(1<<30-1)).Uint64()
		v.Rsh(v, 30)
		port := new(big.Int).And(v, big.NewInt(1<<14-1)).Int64() + portBase
		v.Rsh(v, 14)
		low := v.Uint64() & 0xffff
		return ParsedCode{Short: true, Low16: uint16(low), Port: int(port), Secret: uint32(secret)}, nil
	case 16:
		v.Rsh(v, 2)
		secret := new(big.Int).And(v, big.NewInt(1<<30-1)).Uint64()
		v.Rsh(v, 30)
		port := new(big.Int).And(v, big.NewInt(0xffff)).Int64()
		v.Rsh(v, 16)
		ipv := uint32(v.Uint64())
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, ipv)
		return ParsedCode{FullIP: ip, Port: int(port), Secret: uint32(secret)}, nil
	default:
		return ParsedCode{}, fmt.Errorf("code has %d characters, expected 12 or 16", n)
	}
}

// Candidates lists the Controller addresses to try for a parsed code.
func (p ParsedCode) Candidates() []string {
	if !p.Short {
		return []string{net.JoinHostPort(p.FullIP.String(), fmt.Sprint(p.Port))}
	}
	seen := map[string]bool{}
	var out []string
	for _, ip := range LocalIPv4s() {
		c := net.IPv4(ip[0], ip[1], byte(p.Low16>>8), byte(p.Low16))
		a := net.JoinHostPort(c.String(), fmt.Sprint(p.Port))
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

// ReturnCode is what the phone page shows in flow B: its IPv4, its ICE port and
// a 12-bit check keyed by the token (catches typos and blind guesses).
func ReturnCode(token []byte, ip net.IP, port int) string {
	ip4 := ip.To4()
	chk := returnCheck(token, ip4, port)
	v := new(big.Int).SetUint64(uint64(binary.BigEndian.Uint32(ip4)))
	v.Lsh(v, 16)
	v.Or(v, big.NewInt(int64(port)))
	v.Lsh(v, 12)
	v.Or(v, big.NewInt(int64(chk)))
	return EncodeBits(v, 12)
}

func returnCheck(token []byte, ip4 net.IP, port int) uint16 {
	m := hmac.New(sha256.New, token)
	m.Write([]byte("wm-return"))
	m.Write(ip4)
	m.Write([]byte{byte(port >> 8), byte(port)})
	s := m.Sum(nil)
	return (uint16(s[0])<<8 | uint16(s[1])) >> 4
}

// ParseReturnCode checks and decodes the phone's return code.
func ParseReturnCode(token []byte, s string) (net.IP, int, error) {
	v, n, err := DecodeCode(s)
	if err != nil {
		return nil, 0, err
	}
	if n != 12 {
		return nil, 0, fmt.Errorf("code has %d characters, expected 12", n)
	}
	chk := uint16(new(big.Int).And(v, big.NewInt(0xfff)).Uint64())
	v.Rsh(v, 12)
	port := int(new(big.Int).And(v, big.NewInt(0xffff)).Int64())
	v.Rsh(v, 16)
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, uint32(v.Uint64()))
	if returnCheck(token, ip, port) != chk {
		return nil, 0, errors.New("check failed: typo, or a code from another session")
	}
	return ip, port, nil
}

// LocalIPv4s returns the non-loopback IPv4 addresses of interfaces that are up.
func LocalIPv4s() []net.IP {
	var out []net.IP
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ip4 := ipn.IP.To4(); ip4 != nil {
				out = append(out, ip4)
			}
		}
	}
	return out
}

// InterfaceIPv4s is LocalIPv4s with interface names, for display.
func InterfaceIPv4s() [][2]string {
	var out [][2]string
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				out = append(out, [2]string{ifc.Name, ipn.IP.To4().String()})
			}
		}
	}
	return out
}
