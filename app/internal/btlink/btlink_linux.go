package btlink

import (
	"errors"
	"net"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// BlueZ keeps addresses least significant byte first.
func toBlueZ(m [6]byte) (r [6]uint8) {
	for i := range m {
		r[i] = m[5-i]
	}
	return
}

// localMAC asks the kernel for hci0's address (HCIGETDEVINFO).
func localMAC() ([6]byte, error) {
	var mac [6]byte
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.BTPROTO_HCI)
	if err != nil {
		return mac, ErrUnsupported
	}
	defer unix.Close(fd)
	// struct hci_dev_info: u16 dev_id, char name[8], bdaddr_t bdaddr, ...
	var info [128]byte // dev_id 0 = hci0
	const hciGetDevInfo = 0x800448D3
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), hciGetDevInfo, uintptr(unsafe.Pointer(&info[0]))); e != 0 {
		return mac, ErrUnsupported
	}
	for i := 0; i < 6; i++ {
		mac[i] = info[10+5-i]
	}
	if mac == ([6]byte{}) {
		return mac, ErrUnsupported
	}
	return mac, nil
}

type listener struct {
	f    *os.File
	addr Addr
}

func listen() (net.Listener, Addr, error) {
	mac, err := localMAC()
	if err != nil {
		return nil, Addr{}, err
	}
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.BTPROTO_RFCOMM)
	if err != nil {
		return nil, Addr{}, ErrUnsupported
	}
	ch := 0
	for c := 1; c <= 30 && ch == 0; c++ {
		if unix.Bind(fd, &unix.SockaddrRFCOMM{Channel: uint8(c)}) == nil {
			ch = c
		}
	}
	if ch == 0 {
		unix.Close(fd)
		return nil, Addr{}, errors.New("no free RFCOMM channel")
	}
	if err := unix.Listen(fd, 4); err != nil {
		unix.Close(fd)
		return nil, Addr{}, err
	}
	a := Addr{MAC: mac, Channel: ch}
	return &listener{f: os.NewFile(uintptr(fd), "rfcomm"), addr: a}, a, nil
}

func (l *listener) Accept() (net.Conn, error) {
	rc, err := l.f.SyscallConn()
	if err != nil {
		return nil, err
	}
	var nfd int
	var aerr error
	err = rc.Read(func(fd uintptr) bool {
		nfd, _, aerr = unix.Accept4(int(fd), unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK)
		return aerr != unix.EAGAIN
	})
	if err != nil {
		return nil, err
	}
	if aerr != nil {
		return nil, aerr
	}
	return &conn{File: os.NewFile(uintptr(nfd), "rfcomm"), local: l.addr}, nil
}

func (l *listener) Close() error   { return l.f.Close() }
func (l *listener) Addr() net.Addr { return l.addr }

func dial(a Addr, timeout time.Duration) (net.Conn, error) {
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.BTPROTO_RFCOMM)
	if err != nil {
		return nil, ErrUnsupported
	}
	err = unix.Connect(fd, &unix.SockaddrRFCOMM{Addr: toBlueZ(a.MAC), Channel: uint8(a.Channel)})
	if err != nil && err != unix.EINPROGRESS {
		unix.Close(fd)
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "rfcomm")
	if err == unix.EINPROGRESS {
		rc, _ := f.SyscallConn()
		f.SetWriteDeadline(time.Now().Add(timeout))
		var cerr error
		first := true
		werr := rc.Write(func(fd uintptr) bool {
			if first { // called once before the poller waits: still connecting
				first = false
				return false
			}
			v, e := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_ERROR)
			if e != nil {
				cerr = e
			} else if v != 0 {
				cerr = unix.Errno(v)
			}
			return true
		})
		f.SetWriteDeadline(time.Time{})
		if werr != nil {
			f.Close()
			if errors.Is(werr, os.ErrDeadlineExceeded) {
				return nil, timeoutError{}
			}
			return nil, werr
		}
		if cerr != nil {
			f.Close()
			return nil, cerr
		}
	}
	return &conn{File: f, remote: a}, nil
}

// conn is an RFCOMM socket; os.File gives it the runtime poller, so
// deadlines work as on a TCP connection.
type conn struct {
	*os.File
	local, remote Addr
}

func (c *conn) LocalAddr() net.Addr  { return c.local }
func (c *conn) RemoteAddr() net.Addr { return c.remote }
