//go:build !linux

package terminal

// The dev fallback runs pane-host unsandboxed on the host, where loopback is
// already up and there are no ambient capabilities to drop.

func raiseLoopback() error { return nil }

func dropAmbientCaps() {}
