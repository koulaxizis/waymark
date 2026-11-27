package btlink

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Winsock's Bluetooth family (ws2bth.h). A standard user may open these
// sockets; the peer must have been paired once in Settings.
const (
	afBTH          = 32
	bthprotoRFCOMM = 3
	btPortAny      = 0xFFFFFFFF
	sockaddrBTHLen = 30 // packed: family u16, btAddr u64, GUID, port u32
	wsaETimedOut   = 10060
)

var (
	ws2         = windows.NewLazySystemDLL("ws2_32.dll")
	procBind    = ws2.NewProc("bind")
	procConnect = ws2.NewProc("connect")
	procAccept  = ws2.NewProc("accept")
	procGetName = ws2.NewProc("getsockname")
	procRecv    = ws2.NewProc("recv")
	procSend    = ws2.NewProc("send")
	wsaOnce     sync.Once
	wsaErr      error
)

func startup() error {
	wsaOnce.Do(func() {
		var d windows.WSAData
		wsaErr = windows.WSAStartup(uint32(0x202), &d)
	})
	return wsaErr
}

func sockaddr(a Addr, port uint32) [sockaddrBTHLen]byte {
	var b [sockaddrBTHLen]byte
	binary.LittleEndian.PutUint16(b[0:], afBTH)
	var bt uint64
	for _, x := range a.MAC {
		bt = bt<<8 | uint64(x)
	}
	binary.LittleEndian.PutUint64(b[2:], bt)
	binary.LittleEndian.PutUint32(b[26:], port)
	return b
}

func fromSockaddr(b []byte) Addr {
	var a Addr
	bt := binary.LittleEndian.Uint64(b[2:])
	for i := 5; i >= 0; i-- {
		a.MAC[i] = byte(bt)
		bt >>= 8
	}
	a.Channel = int(binary.LittleEndian.Uint32(b[26:]))
	return a
}

// call runs a Winsock function that returns SOCKET_ERROR (-1) on failure.
func call(p *windows.LazyProc, args ...uintptr) (uintptr, error) {
	r, _, e := p.Call(args...) // e is GetLastError, which Winsock sets
	if int32(r) == -1 || r == uintptr(windows.InvalidHandle) {
		return r, e
	}
	return r, nil
}

func socket() (windows.Handle, error) {
	if err := startup(); err != nil {
		return 0, ErrUnsupported
	}
	h, err := windows.Socket(afBTH, windows.SOCK_STREAM, bthprotoRFCOMM)
	if err != nil {
		return 0, ErrUnsupported // no adapter, or Bluetooth turned off
	}
	return h, nil
}

type listener struct {
	h    windows.Handle
	addr Addr
	once sync.Once
}

func listen() (net.Listener, Addr, error) {
	h, err := socket()
	if err != nil {
		return nil, Addr{}, err
	}
	sa := sockaddr(Addr{}, btPortAny)
	if _, err := call(procBind, uintptr(h), uintptr(unsafe.Pointer(&sa[0])), sockaddrBTHLen); err != nil {
		windows.Closesocket(h)
		return nil, Addr{}, ErrUnsupported
	}
	if err := windows.Listen(h, 4); err != nil {
		windows.Closesocket(h)
		return nil, Addr{}, err
	}
	// getsockname gives the local radio and the channel the stack chose.
	var got [sockaddrBTHLen]byte
	n := int32(sockaddrBTHLen)
	if _, err := call(procGetName, uintptr(h), uintptr(unsafe.Pointer(&got[0])), uintptr(unsafe.Pointer(&n))); err != nil {
		windows.Closesocket(h)
		return nil, Addr{}, err
	}
	a := fromSockaddr(got[:])
	if a.MAC == ([6]byte{}) || a.Channel < 1 || a.Channel > 30 {
		windows.Closesocket(h)
		return nil, Addr{}, ErrUnsupported
	}
	return &listener{h: h, addr: a}, a, nil
}

func (l *listener) Accept() (net.Conn, error) {
	var peer [sockaddrBTHLen]byte
	n := int32(sockaddrBTHLen)
	r, err := call(procAccept, uintptr(l.h), uintptr(unsafe.Pointer(&peer[0])), uintptr(unsafe.Pointer(&n)))
	if err != nil {
		return nil, net.ErrClosed
	}
	return newConn(windows.Handle(r), l.addr, fromSockaddr(peer[:])), nil
}

func (l *listener) Close() error {
	l.once.Do(func() { windows.Closesocket(l.h) })
	return nil
}

func (l *listener) Addr() net.Addr { return l.addr }

func dial(a Addr, timeout time.Duration) (net.Conn, error) {
	h, err := socket()
	if err != nil {
		return nil, err
	}
	// A blocking connect cannot be timed, so the timeout closes the socket.
	done := make(chan error, 1)
	go func() {
		sa := sockaddr(a, uint32(a.Channel))
		_, err := call(procConnect, uintptr(h), uintptr(unsafe.Pointer(&sa[0])), sockaddrBTHLen)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			windows.Closesocket(h)
			return nil, err
		}
	case <-time.After(timeout):
		windows.Closesocket(h)
		<-done
		return nil, timeoutError{}
	}
	return newConn(h, Addr{}, a), nil
}

// conn is a blocking RFCOMM socket. Deadlines become SO_RCVTIMEO and
// SO_SNDTIMEO for each call; a timed-out link is closed by its user anyway.
type conn struct {
	h             windows.Handle
	local, remote Addr
	mu            sync.Mutex
	rd, wd        time.Time
	once          sync.Once
	closed        chan struct{}
}

func newConn(h windows.Handle, local, remote Addr) *conn {
	return &conn{h: h, local: local, remote: remote, closed: make(chan struct{})}
}

func (c *conn) timeout(opt int, d time.Time) error {
	ms := 0
	if !d.IsZero() {
		left := time.Until(d)
		if left <= 0 {
			return timeoutError{}
		}
		ms = int(left / time.Millisecond)
		if ms < 1 {
			ms = 1
		}
	}
	windows.SetsockoptInt(c.h, windows.SOL_SOCKET, opt, ms)
	return nil
}

func (c *conn) io(p *windows.LazyProc, opt int, dl func() time.Time, b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	if err := c.timeout(opt, dl()); err != nil {
		return 0, err
	}
	if len(b) > 1<<30 {
		b = b[:1<<30]
	}
	r, err := call(p, uintptr(c.h), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 0)
	if err != nil {
		if errors.Is(err, syscall.Errno(wsaETimedOut)) {
			return 0, timeoutError{}
		}
		select {
		case <-c.closed:
			return 0, net.ErrClosed
		default:
		}
		return 0, err
	}
	return int(r), nil
}

func (c *conn) Read(b []byte) (int, error) {
	n, err := c.io(procRecv, windows.SO_RCVTIMEO, c.readDL, b)
	if err == nil && n == 0 {
		return 0, io.EOF
	}
	return n, err
}

func (c *conn) Write(b []byte) (int, error) {
	sent := 0
	for sent < len(b) {
		n, err := c.io(procSend, windows.SO_SNDTIMEO, c.writeDL, b[sent:])
		if err != nil {
			return sent, err
		}
		sent += n
	}
	return sent, nil
}

func (c *conn) readDL() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.rd }
func (c *conn) writeDL() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.wd }

func (c *conn) Close() error {
	c.once.Do(func() {
		close(c.closed)
		windows.Shutdown(c.h, windows.SHUT_RDWR)
		windows.Closesocket(c.h)
	})
	return nil
}

func (c *conn) LocalAddr() net.Addr  { return c.local }
func (c *conn) RemoteAddr() net.Addr { return c.remote }
func (c *conn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.rd, c.wd = t, t
	c.mu.Unlock()
	return nil
}
func (c *conn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.rd = t
	c.mu.Unlock()
	return nil
}
func (c *conn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.wd = t
	c.mu.Unlock()
	return nil
}
