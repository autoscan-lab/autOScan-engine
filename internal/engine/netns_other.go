//go:build !linux

package engine

// Dev fallback: unsandboxed host, loopback already up, no ambient capabilities to drop.
func RaiseLoopback() error { return nil }

func DropAmbientCaps() {}
