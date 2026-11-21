// Package secure holds the framing and the encrypted channel that Waymark
// devices use after pairing: length-prefixed frames, AES-256-GCM with one key
// per direction and counter nonces, and the small key helpers around them.
package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/hkdf"
)

// MaxFrame bounds one frame. A 64 KB file chunk in base64 JSON fits easily.
const MaxFrame = 1 << 20

// Version is mixed into every derived key, so keys never cross protocol versions.
const Version = "waymark-v1"

var ErrFrameTooLarge = errors.New("frame too large")

// WriteFrame writes a 4-byte big-endian length and the payload in one write.
func WriteFrame(w io.Writer, b []byte) error {
	if len(b) > MaxFrame {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(buf, uint32(len(b)))
	copy(buf[4:], b)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads one length-prefixed frame.
func ReadFrame(r io.Reader) ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(h[:])
	if n > MaxFrame {
		return nil, ErrFrameTooLarge
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

// WriteJSON and ReadJSON are plaintext frames, used only during the handshake.
func WriteJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return WriteFrame(w, b)
}

func ReadJSON(r io.Reader, v any) error {
	b, err := ReadFrame(r)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Derive returns 32 bytes from key with HKDF-SHA256 and a label.
func Derive(key []byte, label string, salt []byte) []byte {
	h := hkdf.New(sha256.New, key, salt, []byte(Version+" "+label))
	out := make([]byte, 32)
	if _, err := io.ReadFull(h, out); err != nil {
		panic(err) // HKDF cannot fail for 32 bytes
	}
	return out
}

// MAC is HMAC-SHA256 over the concatenated parts.
func MAC(key []byte, parts ...[]byte) []byte {
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		m.Write(p)
	}
	return m.Sum(nil)
}

// Equal compares two MACs in constant time.
func Equal(a, b []byte) bool { return hmac.Equal(a, b) }

// SAS turns key material into the 4-digit number both screens show.
func SAS(key []byte, parts ...[]byte) string {
	s := MAC(key, append([][]byte{[]byte("sas")}, parts...)...)
	n := binary.BigEndian.Uint32(s[:4]) % 10000
	return fmt.Sprintf("%04d", n)
}

// RandBytes returns n bytes from the OS random source.
func RandBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// B64 and UnB64 are URL-safe base64 without padding.
func B64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func UnB64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

// Conn is an AES-256-GCM framed channel with counter nonces, one key per
// direction. Send is safe for concurrent use; Recv must have one caller.
type Conn struct {
	c          net.Conn
	send, recv cipher.AEAD
	sendN      uint64
	recvN      uint64
	wmu        sync.Mutex
}

// NewConn sets up the channel. isAgent picks the key direction.
func NewConn(c net.Conn, key []byte, isAgent bool) *Conn {
	mk := func(k []byte) cipher.AEAD {
		b, err := aes.NewCipher(k)
		if err != nil {
			panic(err)
		}
		g, err := cipher.NewGCM(b)
		if err != nil {
			panic(err)
		}
		return g
	}
	a2c, c2a := mk(Derive(key, "a2c", nil)), mk(Derive(key, "c2a", nil))
	s := &Conn{c: c}
	if isAgent {
		s.send, s.recv = a2c, c2a
	} else {
		s.send, s.recv = c2a, a2c
	}
	return s
}

func nonce(n uint64) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint64(b[4:], n)
	return b
}

// SendRaw encrypts and writes one message.
func (s *Conn) SendRaw(b []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	ct := s.send.Seal(nil, nonce(s.sendN), b, nil)
	s.sendN++
	s.c.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return WriteFrame(s.c, ct)
}

// Send encodes v as JSON and sends it.
func (s *Conn) Send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.SendRaw(b)
}

// Recv reads and decrypts one message.
func (s *Conn) Recv() ([]byte, error) {
	ct, err := ReadFrame(s.c)
	if err != nil {
		return nil, err
	}
	pt, err := s.recv.Open(nil, nonce(s.recvN), ct, nil)
	if err != nil {
		return nil, errors.New("decryption failed")
	}
	s.recvN++
	return pt, nil
}

// SetReadDeadline passes through to the socket.
func (s *Conn) SetReadDeadline(t time.Time) error { return s.c.SetReadDeadline(t) }

// RemoteAddr is the peer address.
func (s *Conn) RemoteAddr() net.Addr { return s.c.RemoteAddr() }

func (s *Conn) Close() error { return s.c.Close() }
