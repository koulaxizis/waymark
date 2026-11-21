package wm

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"strconv"
)

func itoa(i int) string { return strconv.Itoa(i) }

// RandBytes returns n random bytes.
func RandBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

// NewID returns a short random message id.
func NewID() string { return hex.EncodeToString(RandBytes(4)) }

// B64 / UnB64 use unpadded base64url, the same as the phone page.
func B64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func UnB64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }
