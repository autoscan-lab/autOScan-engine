package engine

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

const (
	bubblewrap = "bwrap"
	prlimitCmd = "prlimit"
)

const (
	limitCPUSeconds = 20
	limitFileBytes  = 256 << 20
	limitOpenFiles  = 256
	limitProcs      = 128
	cgroupMemBytes  = 512 << 20
)

const memoryCgroupRoot = "/sys/fs/cgroup/memory"

type sandboxSpec struct {
	workDir  string
	readOnly []string
	netAdmin bool
}

func sandboxAvailable() bool {
	_, err := exec.LookPath(bubblewrap)
	return err == nil
}

func existingPaths(paths ...string) []string {
	var out []string
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func InteractiveSandbox(workDir string, cmd []string) (argv []string, cleanup func(), sandboxed bool) {
	if !sandboxAvailable() {
		return cmd, func() {}, false
	}
	// netAdmin lets pane-host raise the namespace loopback so panes can talk over 127.0.0.1.
	argv, cleanup = sandboxCommand(sandboxSpec{workDir: workDir, netAdmin: true}, cmd)
	return argv, cleanup, true
}

func sandboxCommand(spec sandboxSpec, cmd []string) (argv []string, cleanup func()) {
	argv = sandboxArgv(spec, cmd)
	cleanup = func() {}
	if dir, procs, ok := newMemoryCgroup(); ok {
		argv = cgroupJoinArgv(procs, argv)
		cleanup = func() { removeCgroup(dir) }
	}
	return argv, cleanup
}

// rmdir fails with EBUSY while the kernel is still reaping the sandboxed process tree.
func removeCgroup(dir string) {
	for i := 0; i < 50; i++ {
		if err := os.Remove(dir); err == nil || os.IsNotExist(err) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func sandboxArgv(spec sandboxSpec, cmd []string) []string {
	argv := []string{
		bubblewrap,
		"--unshare-all",
		"--die-with-parent",
		"--ro-bind", "/usr", "/usr",
		"--symlink", "usr/bin", "/bin",
		"--symlink", "usr/sbin", "/sbin",
		"--symlink", "usr/lib", "/lib",
		"--symlink", "usr/lib64", "/lib64",
		"--proc", "/proc",
		"--dev", "/dev",
		// bwrap's --dev provides /dev/shm but no mqueue instance.
		"--mqueue", "/dev/mqueue",
		"--tmpfs", "/tmp",
	}
	if spec.netAdmin {
		argv = append(argv, "--cap-add", "CAP_NET_ADMIN")
	}
	for _, ro := range spec.readOnly {
		argv = append(argv, "--ro-bind", ro, ro)
	}
	argv = append(argv, "--bind", spec.workDir, spec.workDir, "--chdir", spec.workDir, "--")
	argv = append(argv, cmd...)

	if _, err := exec.LookPath(prlimitCmd); err == nil {
		argv = append([]string{
			prlimitCmd,
			fmt.Sprintf("--cpu=%d", limitCPUSeconds),
			fmt.Sprintf("--fsize=%d", limitFileBytes),
			fmt.Sprintf("--nofile=%d", limitOpenFiles),
			fmt.Sprintf("--nproc=%d", limitProcs),
			"--",
		}, argv...)
	}
	return argv
}

func newMemoryCgroup() (dir, procsFile string, ok bool) {
	if _, err := os.Stat(memoryCgroupRoot); err != nil {
		return "", "", false
	}
	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", "", false
	}
	dir = filepath.Join(memoryCgroupRoot, "autoscan-"+hex.EncodeToString(token[:]))
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", "", false
	}
	limit := []byte(strconv.Itoa(cgroupMemBytes))
	if err := os.WriteFile(filepath.Join(dir, "memory.limit_in_bytes"), limit, 0); err != nil {
		_ = os.Remove(dir)
		return "", "", false
	}
	return dir, filepath.Join(dir, "cgroup.procs"), true
}

func cgroupJoinArgv(procsFile string, argv []string) []string {
	script := "echo $$ > " + procsFile + `; exec "$@"`
	return append([]string{"sh", "-c", script, "sh"}, argv...)
}
