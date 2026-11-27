//go:build !linux && !windows

package btlink

import (
	"net"
	"time"
)

func listen() (net.Listener, Addr, error)        { return nil, Addr{}, ErrUnsupported }
func dial(Addr, time.Duration) (net.Conn, error) { return nil, ErrUnsupported }
