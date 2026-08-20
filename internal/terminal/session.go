package terminal

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	internalengine "github.com/autoscan-lab/autoscan-engine/internal/engine"
)

const (
	maxSessions      = 5
	idleTimeout      = 10 * time.Minute
	maxLifetime      = 45 * time.Minute
	linger           = 60 * time.Second
	readyTimeout     = 15 * time.Second
)

var (
	ErrSessionLimit  = errors.New("terminal session limit reached")
	ErrPaneLimit     = errors.New("pane limit reached")
	ErrSessionClosed = errors.New("terminal session ended")
)

type Session struct {
	id string

	scratch   string
	ctlPath   string
	ctlAltDir string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	cleanup   func()
	maxPanes  int
	idle      *time.Timer
	lifetime  *time.Timer

	ready     chan struct{}
	createErr error
	procDone  chan struct{}

	mu        sync.Mutex
	panes     int
	closed    bool
	lingering *time.Timer
	reason    string

	done      chan struct{}
	closeOnce sync.Once
}

func (ts *Session) ID() string { return ts.id }

var sessions = struct {
	sync.Mutex
	m map[string]*Session
}{m: map[string]*Session{}}

func Join(claims Claims, buildScratch func() (string, error)) (*Session, error) {
	for attempt := 0; attempt < 2; attempt++ {
		ts, creator, err := getOrCreate(claims)
		if err != nil {
			return nil, err
		}

		if creator {
			ts.create(claims.Student, buildScratch)
		} else {
			<-ts.ready
		}
		if ts.createErr != nil {
			return nil, ts.createErr
		}

		if err := ts.attach(); err != nil {
			if errors.Is(err, ErrSessionClosed) {
				continue
			}
			return nil, err
		}
		return ts, nil
	}
	return nil, ErrSessionClosed
}

func getOrCreate(claims Claims) (ts *Session, creator bool, err error) {
	sessions.Lock()
	defer sessions.Unlock()

	if existing, ok := sessions.m[claims.SessionID]; ok {
		return existing, false, nil
	}
	if len(sessions.m) >= maxSessions {
		return nil, false, ErrSessionLimit
	}

	ts = &Session{
		id:       claims.SessionID,
		maxPanes: claims.Panes,
		ready:    make(chan struct{}),
		procDone: make(chan struct{}),
		done:     make(chan struct{}),
	}
	sessions.m[claims.SessionID] = ts
	return ts, true, nil
}

func (ts *Session) create(student string, buildScratch func() (string, error)) {
	defer close(ts.ready)

	scratch, err := buildScratch()
	if err != nil {
		log.Printf("terminal: scratch setup failed session=%s: %v", ts.id, err)
		ts.fail(errors.New("could not prepare workspace"))
		return
	}
	ts.scratch = scratch

	if err := ts.startPaneHost(student); err != nil {
		log.Printf("terminal: pane-host start failed session=%s: %v", ts.id, err)
		_ = os.RemoveAll(scratch)
		ts.fail(errors.New("could not start shell"))
		return
	}

	ts.idle = time.AfterFunc(idleTimeout, func() {
		ts.teardown("session closed after inactivity")
	})
	ts.lifetime = time.AfterFunc(maxLifetime, func() {
		ts.teardown("session time limit reached")
	})

	go func() {
		<-ts.procDone
		ts.teardown("session ended")
	}()
}

func (ts *Session) fail(err error) {
	ts.createErr = err
	sessions.Lock()
	if sessions.m[ts.id] == ts {
		delete(sessions.m, ts.id)
	}
	sessions.Unlock()
}

func (ts *Session) startPaneHost(student string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable: %w", err)
	}

	ctlDir := filepath.Join(ts.scratch, ".autoscan")
	if err := os.MkdirAll(ctlDir, 0o700); err != nil {
		return err
	}
	ctlPath := filepath.Join(ctlDir, "ctl.sock")
	// sun_path caps out around 104 bytes on darwin, so overlong paths fall back to a short /tmp dir.
	if len(ctlPath) > 100 {
		alt, err := os.MkdirTemp("/tmp", "ast-ctl-*")
		if err != nil {
			return err
		}
		ts.ctlAltDir = alt
		ctlPath = filepath.Join(alt, "ctl.sock")
	}
	ts.ctlPath = ctlPath

	argv, cleanup, sandboxed := internalengine.InteractiveSandbox(ts.scratch, []string{exe, "pane-host", ctlPath})
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = ts.scratch
	cmd.Env = shellEnv(ts.scratch, student)
	// Own session+pgid so kill(-pid) reaps bwrap, its init, and pane-host.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cleanup()
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cleanup()
		return err
	}

	if err := cmd.Start(); err != nil {
		cleanup()
		return err
	}
	ts.cmd = cmd
	ts.stdin = stdin
	ts.cleanup = cleanup

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Printf("terminal: pane-host[%s]: %s", ts.id, scanner.Text())
		}
	}()

	readyCh := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == ReadyLine {
				readyCh <- true
				// Keep draining so pane-host never blocks on a full pipe.
				for scanner.Scan() {
				}
				return
			}
		}
		readyCh <- false
	}()

	go func() {
		_ = cmd.Wait()
		close(ts.procDone)
	}()

	select {
	case ok := <-readyCh:
		if ok {
			if !sandboxed {
				log.Printf("terminal: session %s running unsandboxed (bwrap unavailable)", ts.id)
			}
			return nil
		}
	case <-time.After(readyTimeout):
	case <-ts.procDone:
	}

	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	<-ts.procDone
	cleanup()
	ts.cleanup = nil
	return errors.New("pane-host did not become ready")
}

func (ts *Session) attach() error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.closed {
		return ErrSessionClosed
	}
	if ts.panes >= ts.maxPanes {
		return ErrPaneLimit
	}
	ts.panes++
	if ts.lingering != nil {
		ts.lingering.Stop()
		ts.lingering = nil
	}
	return nil
}

func (ts *Session) detach() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.panes--
	if ts.panes <= 0 && !ts.closed && ts.lingering == nil {
		ts.lingering = time.AfterFunc(linger, func() {
			ts.teardown("session closed")
		})
	}
}

func (ts *Session) touch() {
	if ts.idle != nil {
		ts.idle.Reset(idleTimeout)
	}
}

func (ts *Session) closeReason() string {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.reason == "" {
		return "session ended"
	}
	return ts.reason
}

func (ts *Session) teardown(reason string) {
	ts.closeOnce.Do(func() {
		ts.mu.Lock()
		ts.closed = true
		ts.reason = reason
		if ts.lingering != nil {
			ts.lingering.Stop()
		}
		ts.mu.Unlock()
		if ts.idle != nil {
			ts.idle.Stop()
		}
		if ts.lifetime != nil {
			ts.lifetime.Stop()
		}

		sessions.Lock()
		if sessions.m[ts.id] == ts {
			delete(sessions.m, ts.id)
		}
		sessions.Unlock()

		close(ts.done)

		// Stdin EOF makes pane-host HUP its shells and exit gracefully.
		if ts.stdin != nil {
			_ = ts.stdin.Close()
		}
		select {
		case <-ts.procDone:
		case <-time.After(2 * time.Second):
		}
		if ts.cmd != nil && ts.cmd.Process != nil {
			_ = syscall.Kill(-ts.cmd.Process.Pid, syscall.SIGKILL)
		}
		select {
		case <-ts.procDone:
		case <-time.After(10 * time.Second):
			log.Printf("terminal: session %s did not exit after SIGKILL", ts.id)
		}

		if ts.cleanup != nil {
			ts.cleanup()
		}
		if ts.scratch != "" {
			_ = os.RemoveAll(ts.scratch)
		}
		if ts.ctlAltDir != "" {
			_ = os.RemoveAll(ts.ctlAltDir)
		}
		log.Printf("terminal: session end id=%s (%s)", ts.id, reason)
	})
}

func TeardownAll(reason string) {
	sessions.Lock()
	all := make([]*Session, 0, len(sessions.m))
	for _, ts := range sessions.m {
		all = append(all, ts)
	}
	sessions.Unlock()

	for _, ts := range all {
		ts.teardown(reason)
	}
}

func shellEnv(homeDir, student string) []string {
	return append(
		internalengine.MinimalEnv(homeDir),
		"TERM=xterm-256color",
		`PS1=autoscan@`+promptLabel(student)+`:\w$ `,
	)
}

func promptLabel(student string) string {
	var out []rune
	for _, r := range strings.ToLower(strings.TrimSpace(student)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			out = append(out, r)
		case r == ' ':
			out = append(out, '-')
		}
		if len(out) >= 32 {
			break
		}
	}
	if len(out) == 0 {
		return "submission"
	}
	return string(out)
}
