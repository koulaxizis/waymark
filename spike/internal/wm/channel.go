package wm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"golang.org/x/crypto/hkdf"
)

const maxFrame = 1 << 20

// WriteFrame writes a 4-byte length prefix and the payload.
func WriteFrame(w io.Writer, b []byte) error {
	if len(b) > maxFrame {
		return errors.New("frame too large")
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(b)))
	if _, err := w.Write(append(h[:], b...)); err != nil {
		return err
	}
	return nil
}

// ReadFrame reads one length-prefixed frame.
func ReadFrame(r io.Reader) ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(h[:])
	if n > maxFrame {
		return nil, errors.New("frame too large")
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

// WriteJSON / ReadJSON are plaintext frames, used only before keys exist.
func WriteJSON(w io.Writer, v any) error {
	b, _ := json.Marshal(v)
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
	h := hkdf.New(sha256.New, key, salt, []byte("waymark-spike-v1 "+label))
	out := make([]byte, 32)
	io.ReadFull(h, out)
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

// SAS turns key material into the 4-digit confirmation number.
func SAS(key []byte, parts ...[]byte) string {
	s := MAC(key, append([][]byte{[]byte("sas")}, parts...)...)
	n := (uint32(s[0])<<24 | uint32(s[1])<<16 | uint32(s[2])<<8 | uint32(s[3])) % 10000
	return fmt.Sprintf("%04d", n)
}

// SecureConn is an AES-256-GCM framed channel over TCP with counter nonces,
// one key per direction.
type SecureConn struct {
	c          net.Conn
	send, recv cipher.AEAD
	sendN      uint64
	recvN      uint64
	wmu        sync.Mutex
}

// NewSecureConn sets up the channel. isAgent picks the key direction.
func NewSecureConn(c net.Conn, key []byte, isAgent bool) *SecureConn {
	a2c := Derive(key, "a2c", nil)
	c2a := Derive(key, "c2a", nil)
	mk := func(k []byte) cipher.AEAD {
		b, _ := aes.NewCipher(k)
		g, _ := cipher.NewGCM(b)
		return g
	}
	s := &SecureConn{c: c}
	if isAgent {
		s.send, s.recv = mk(a2c), mk(c2a)
	} else {
		s.send, s.recv = mk(c2a), mk(a2c)
	}
	return s
}

func nonce(n uint64) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint64(b[4:], n)
	return b
}

// Send encrypts and writes one JSON message.
func (s *SecureConn) Send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	ct := s.send.Seal(nil, nonce(s.sendN), b, nil)
	s.sendN++
	return WriteFrame(s.c, ct)
}

// Recv reads and decrypts one message. Only one goroutine may call it.
func (s *SecureConn) Recv() ([]byte, error) {
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

func (s *SecureConn) Close() error { return s.c.Close() }
