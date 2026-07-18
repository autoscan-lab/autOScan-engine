package terminal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// RunPaneHost is the terminal session's payload — it runs inside the session's
// bubblewrap sandbox when available, raw in dev. It owns every pane's bash:
// the engine connects per pane over the unix control socket, sends the initial
// size, and receives the PTY master fd back via SCM_RIGHTS.
//
// Inside the sandbox, bwrap's own init is PID 1 (it reaps orphans); pane-host
// is a normal process, so losing it kills the namespace and every pane with it.

// ReadyLine is printed on stdout once the control socket is listening.
const ReadyLine = "PANEHOST_READY"

func RunPaneHost(ctlPath string) int {
	log.SetPrefix("pane-host: ")
	log.SetFlags(0)

	// Namespace prep: loopback up for 127.0.0.1 between panes, then drop the
	// ambient CAP_NET_ADMIN before any shell starts. Loopback failure is not
	// fatal — FIFOs, unix sockets, and SysV IPC still work without it.
	if err := raiseLoopback(); err != nil {
		log.Printf("loopback: %v", err)
	}
	dropAmbientCaps()

	_ = os.Remove(ctlPath)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: ctlPath, Net: "unix"})
	if err != nil {
		log.Printf("listen: %v", err)
		return 1
	}
	defer listener.Close()

	panes := &paneSet{procs: map[int]*os.Process{}}
	shutdown := func() {
		panes.killAll()
		os.Exit(0)
	}

	// stdin EOF means the engine (or the sandbox) went away: kill everything.
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		shutdown()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		shutdown()
	}()

	// The engine waits for this line on stdout before dialing the socket.
	fmt.Println(ReadyLine)

	cwd, err := os.Getwd()
	if err != nil {
		log.Printf("getwd: %v", err)
		return 1
	}

	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			return 0
		}
		go panes.serve(conn, cwd)
	}
}

type paneSet struct {
	mu    sync.Mutex
	procs map[int]*os.Process
}

func (h *paneSet) add(p *os.Process) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.procs[p.Pid] = p
}

func (h *paneSet) remove(pid int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.procs, pid)
}

// killAll hangs up every pane shell (bash forwards SIGHUP to its jobs), then
// force-kills the process groups shortly after. Only called on teardown, so
// PID-reuse concerns don't apply — everything is going away.
func (h *paneSet) killAll() {
	h.mu.Lock()
	procs := make([]*os.Process, 0, len(h.procs))
	for _, p := range h.procs {
		procs = append(procs, p)
	}
	h.mu.Unlock()

	for _, p := range procs {
		_ = syscall.Kill(-p.Pid, syscall.SIGHUP)
		_ = syscall.Kill(p.Pid, syscall.SIGHUP)
	}
	time.Sleep(300 * time.Millisecond)
	for _, p := range procs {
		_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
	}
}

// serve handles one pane: open request → bash on a fresh PTY → master fd back
// to the engine → wait for either the shell to exit (report it) or the engine
// to close the connection (hang up the shell).
func (h *paneSet) serve(conn *net.UnixConn, cwd string) {
	defer conn.Close()

	line, err := bufio.NewReaderSize(conn, 4096).ReadBytes('\n')
	if err != nil {
		return
	}
	var open paneOpen
	if json.Unmarshal(line, &open) != nil || open.V != 1 {
		_ = writePaneReply(conn, paneReply{OK: false, Err: "bad open request"}, -1)
		return
	}
	if open.Cols == 0 {
		open.Cols = 80
	}
	if open.Rows == 0 {
		open.Rows = 24
	}

	cmd := exec.Command("bash")
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: open.Cols, Rows: open.Rows})
	if err != nil {
		_ = writePaneReply(conn, paneReply{OK: false, Err: err.Error()}, -1)
		return
	}
	h.add(cmd.Process)

	pid := cmd.Process.Pid
	replyErr := writePaneReply(conn, paneReply{OK: true, Pid: pid}, int(ptmx.Fd()))
	// The engine holds its own copy of the master now (or the send failed).
	_ = ptmx.Close()

	exited := make(chan struct{})
	exitCode := 0
	go func() {
		_ = cmd.Wait()
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		h.remove(pid)
		close(exited)
	}()

	if replyErr != nil {
		hangupPane(pid, exited)
		return
	}

	// No further engine→pane-host traffic exists, so a Read unblocking means
	// the connection closed (pane teardown).
	connClosed := make(chan struct{})
	go func() {
		_, _ = conn.Read(make([]byte, 1))
		close(connClosed)
	}()

	select {
	case <-exited:
		payload, err := json.Marshal(paneEvent{Event: "exit", Code: exitCode})
		if err == nil {
			_, _ = conn.Write(append(payload, '\n'))
		}
	case <-connClosed:
		hangupPane(pid, exited)
	}
}

// hangupPane HUPs the shell's process group and escalates to SIGKILL if it is
// still running two seconds later. Waiting on `exited` (not a bare timer)
// before the KILL avoids signalling a reused PID; the post-KILL wait is
// bounded because a shell wedged in the kernel's PTY-drain exit path (its
// master fd still open elsewhere) cannot be forced from here.
func hangupPane(pid int, exited <-chan struct{}) {
	_ = syscall.Kill(-pid, syscall.SIGHUP)
	_ = syscall.Kill(pid, syscall.SIGHUP)
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			log.Printf("pane %d did not exit after SIGKILL", pid)
		}
	}
}
