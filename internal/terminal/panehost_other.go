//go:build !linux

package terminal

// Dev fallback: unsandboxed host, loopback already up, no ambient capabilities to drop.
func raiseLoopback() error { return nil }

func dropAmbientCaps() {}
