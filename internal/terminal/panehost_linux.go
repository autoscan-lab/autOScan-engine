//go:build linux

package terminal

import "golang.org/x/sys/unix"

// raiseLoopback brings lo up inside the sandbox's fresh network namespace so
// panes can reach each other over 127.0.0.1. Needs the ambient CAP_NET_ADMIN
// the engine grants via bwrap --cap-add; a no-op outside the sandbox where lo
// is already up (SIOCSIFFLAGS with unchanged flags).
func raiseLoopback() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	ifreq, err := unix.NewIfreq("lo")
	if err != nil {
		return err
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, ifreq); err != nil {
		return err
	}
	flags := ifreq.Uint16()
	if flags&unix.IFF_UP != 0 {
		return nil
	}
	ifreq.SetUint16(flags | unix.IFF_UP | unix.IFF_RUNNING)
	return unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, ifreq)
}

// dropAmbientCaps clears the ambient capability set so pane shells and student
// processes inherit no capabilities.
func dropAmbientCaps() {
	_ = unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0)
}
