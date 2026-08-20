//go:build linux

package terminal

import "golang.org/x/sys/unix"

// Needs the ambient CAP_NET_ADMIN the engine grants via bwrap --cap-add; a no-op where lo is already up.
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

func dropAmbientCaps() {
	_ = unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0)
}
