//go:build !windows

package main

// keepAwake and moveToProjector come for macOS and Linux in a later version.
func keepAwake(bool) {}

func moveToProjector(string) {}
