package tests

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/autoscan-lab/autoscan-engine/internal/terminal"
)

// The test binary doubles as the pane-host so the integration test exercises
// the real re-exec path (the engine spawns os.Executable() the same way).
func TestMain(m *testing.M) {
	if len(os.Args) >= 3 && os.Args[1] == "pane-host" {
		os.Exit(terminal.RunPaneHost(os.Args[2]))
	}
	os.Exit(m.Run())
}

// alive reports whether pid exists (signal 0 probe; zombies count until reaped).
func alive(pid int) bool {
	return unix.Kill(pid, 0) == nil
}

func waitUntil(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// startPaneHost re-execs this test binary in pane-host mode and waits for its
// readiness line. Returns the socket path and the stdin lifeline.
func startPaneHost(t *testing.T, scratch string) (sock string, stdin *os.File, cmd *exec.Cmd) {
	t.Helper()

	sock = filepath.Join(scratch, "ctl.sock")
	if len(sock) > 100 {
		alt, err := os.MkdirTemp("/tmp", "ast-test-*")
		if err != nil {
			t.Fatalf("mkdtemp: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(alt) })
		sock = filepath.Join(alt, "ctl.sock")
	}

	cmd = exec.Command(os.Args[0], "pane-host", sock)
	cmd.Dir = scratch
	cmd.Env = os.Environ()
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pane-host: %v", err)
	}
	t.Cleanup(func() {
		_ = stdinPipe.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	readyCh := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == terminal.ReadyLine {
				readyCh <- true
				return
			}
		}
		readyCh <- false
	}()
	select {
	case ok := <-readyCh:
		if !ok {
			t.Fatal("pane-host exited before READY")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pane-host never became ready")
	}

	return sock, stdinPipe.(*os.File), cmd
}

func TestPaneHostSession(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	scratch := t.TempDir()
	sock, stdin, cmd := startPaneHost(t, scratch)

	openPane := func() (*os.File, *net.UnixConn, int) {
		t.Helper()
		master, ctl, pid, err := terminal.OpenPane(sock, 100, 30)
		if err != nil {
			t.Fatalf("open pane: %v", err)
		}
		// Drain the master like the engine's output pump does — an unread PTY
		// can wedge a HUP'd shell in the kernel's drain-on-exit path.
		go func() {
			buf := make([]byte, 4096)
			for {
				if _, err := master.Read(buf); err != nil {
					return
				}
			}
		}()
		return master, ctl, pid
	}

	master1, ctl1, pid1 := openPane()
	defer master1.Close()
	defer ctl1.Close()
	master2, ctl2, pid2 := openPane()

	if pid1 == pid2 {
		t.Fatalf("panes share a pid: %d", pid1)
	}

	// Shared workspace: a file touched in pane 1 appears in the scratch both
	// shells run in.
	if _, err := master1.Write([]byte("touch marker\n")); err != nil {
		t.Fatalf("write pane 1: %v", err)
	}
	waitUntil(t, 5*time.Second, "marker file", func() bool {
		_, err := os.Stat(filepath.Join(scratch, "marker"))
		return err == nil
	})

	// Pane teardown, in the engine's finish() order: master first, then the
	// control connection — pane-host then reaps the shell.
	_ = master2.Close()
	_ = ctl2.Close()
	waitUntil(t, 10*time.Second, "pane 2 to die", func() bool { return !alive(pid2) })
	if !alive(pid1) {
		t.Fatal("pane 1 died with pane 2")
	}

	// A shell exiting on its own is reported as an exit event on the lifeline.
	if _, err := master1.Write([]byte("exit\n")); err != nil {
		t.Fatalf("write exit: %v", err)
	}
	_ = ctl1.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(ctl1).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read exit event: %v", err)
	}
	var event struct {
		Event string `json:"event"`
	}
	if json.Unmarshal(line, &event) != nil || event.Event != "exit" {
		t.Fatalf("event = %s", line)
	}

	// stdin EOF tears everything down.
	master3, ctl3, pid3 := openPane()
	defer master3.Close()
	defer ctl3.Close()
	_ = stdin.Close()
	waitUntil(t, 5*time.Second, "pane 3 to die", func() bool { return !alive(pid3) })

	// Wait (not a liveness poll) — the exited pane-host stays a zombie until
	// this test, its parent, reaps it.
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("pane-host did not exit after stdin EOF")
	}
}

func TestPaneHostRejectsBadOpen(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	sock, _, _ := startPaneHost(t, t.TempDir())

	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(`{"v":9}` + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	var reply struct {
		OK  bool   `json:"ok"`
		Err string `json:"err"`
	}
	if json.Unmarshal(line, &reply) != nil || reply.OK || reply.Err == "" {
		t.Fatalf("reply = %s", line)
	}
}
