package terminal

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Pane-host control protocol: newline-delimited JSON over the session's unix
// socket, with the PTY master fd riding the reply as SCM_RIGHTS. Each control
// connection is one pane's lifeline — closing it tears the pane down.

type paneOpen struct {
	V    int    `json:"v"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type paneReply struct {
	OK  bool   `json:"ok"`
	Pid int    `json:"pid,omitempty"`
	Err string `json:"err,omitempty"`
}

type paneEvent struct {
	Event string `json:"event"`
	Code  int    `json:"code"`
}

// writePaneReply sends one newline-terminated JSON reply, attaching fd (the
// PTY master) as SCM_RIGHTS when non-negative.
func writePaneReply(conn *net.UnixConn, reply paneReply, fd int) error {
	payload, err := json.Marshal(reply)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')

	if fd < 0 {
		_, err = conn.Write(payload)
		return err
	}

	n, _, err := conn.WriteMsgUnix(payload, unix.UnixRights(fd), nil)
	if err != nil {
		return err
	}
	if n < len(payload) {
		_, err = conn.Write(payload[n:])
	}
	return err
}

// readPaneReply reads pane-host's newline-terminated JSON reply, collecting
// any SCM_RIGHTS fds that ride along.
func readPaneReply(conn *net.UnixConn) (paneReply, []int, error) {
	buf := make([]byte, 4096)
	oob := make([]byte, unix.CmsgSpace(4))
	var (
		line []byte
		fds  []int
	)
	for {
		n, oobn, flags, _, err := conn.ReadMsgUnix(buf, oob)
		if err != nil {
			return paneReply{}, fds, err
		}
		if flags&unix.MSG_CTRUNC != 0 {
			return paneReply{}, fds, errors.New("pane control message truncated")
		}
		if oobn > 0 {
			msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
			if err != nil {
				return paneReply{}, fds, err
			}
			for _, msg := range msgs {
				got, err := unix.ParseUnixRights(&msg)
				if err != nil {
					continue
				}
				fds = append(fds, got...)
			}
		}
		line = append(line, buf[:n]...)
		if idx := bytes.IndexByte(line, '\n'); idx >= 0 {
			var reply paneReply
			if err := json.Unmarshal(line[:idx], &reply); err != nil {
				return paneReply{}, fds, err
			}
			return reply, fds, nil
		}
		if n == 0 {
			return paneReply{}, fds, errors.New("pane reply truncated")
		}
	}
}

// OpenPane asks the pane-host behind ctlPath for a fresh shell PTY. It returns
// the master (non-blocking, poller-registered so Close interrupts reads), the
// control connection that acts as the pane's lifeline, and the shell's pid as
// seen by pane-host.
func OpenPane(ctlPath string, cols, rows uint16) (*os.File, *net.UnixConn, int, error) {
	addr := &net.UnixAddr{Name: ctlPath, Net: "unix"}
	var (
		conn *net.UnixConn
		err  error
	)
	for i := 0; i < 3; i++ {
		conn, err = net.DialUnix("unix", nil, addr)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return nil, nil, 0, err
	}

	// Bound the handshake so a wedged pane-host can't hang the caller.
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	payload, err := json.Marshal(paneOpen{V: 1, Cols: cols, Rows: rows})
	if err != nil {
		conn.Close()
		return nil, nil, 0, err
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		conn.Close()
		return nil, nil, 0, err
	}

	reply, fds, err := readPaneReply(conn)
	if err != nil || !reply.OK || len(fds) != 1 {
		for _, fd := range fds {
			_ = unix.Close(fd)
		}
		conn.Close()
		if err == nil && !reply.OK {
			err = errors.New(reply.Err)
		}
		if err == nil {
			err = errors.New("pane reply carried no fd")
		}
		return nil, nil, 0, err
	}

	_ = conn.SetDeadline(time.Time{})

	if err := unix.SetNonblock(fds[0], true); err != nil {
		_ = unix.Close(fds[0])
		conn.Close()
		return nil, nil, 0, err
	}
	return os.NewFile(uintptr(fds[0]), "pane-pty"), conn, reply.Pid, nil
}
