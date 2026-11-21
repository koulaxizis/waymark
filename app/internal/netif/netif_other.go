//go:build !windows && !linux && !darwin

package netif

func kindsByIndex() map[int]string { return map[int]string{} }
