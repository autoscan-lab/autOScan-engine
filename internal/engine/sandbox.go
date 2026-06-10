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

// Resource ceilings for sandboxed processes. The wall-clock timeout is the
// primary bound; these stop a submission exhausting the host another way.
const (
	limitCPUSeconds = 20
	limitFileBytes  = 256 << 20
	limitOpenFiles  = 256
	limitProcs      = 128
	cgroupMemBytes  = 512 << 20
)

// memoryCgroupRoot is the v1 memory controller cgroup hierarchy.
const memoryCgroupRoot = "/sys/fs/cgroup/memory"

// sandboxSpec describes one sandboxed invocation.
type sandboxSpec struct {
	workDir  string   // bound read-write; the process's working directory
	readOnly []string // extra host paths bound read-only
}

// sandboxAvailable reports whether the bubblewrap launcher is installed. When
// it is not, callers run the command directly without the sandbox.
func sandboxAvailable() bool {
	_, err := exec.LookPath(bubblewrap)
	return err == nil
}

// existingPaths returns the non-empty paths that exist on disk.
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

// sandboxCommand builds the argv to run cmd inside bubblewrap with resource
// InteractiveSandbox builds the argv and cleanup to run an interactive command
// (e.g. a shell on a PTY) inside the same bubblewrap sandbox used for graded
// submissions: workDir bound read-write, no network, prlimit + memory cgroup
// ceilings. sandboxed is false when bubblewrap is unavailable, in which case
// the caller should run cmd directly.
func InteractiveSandbox(workDir string, cmd []string) (argv []string, cleanup func(), sandboxed bool) {
	if !sandboxAvailable() {
		return cmd, func() {}, false
	}
	argv, cleanup = sandboxCommand(sandboxSpec{workDir: workDir}, cmd)
	return argv, cleanup, true
}

// sandboxCommand builds the argv to run cmd inside bubblewrap with resource
// limits and a memory cgroup. cleanup must be called once the process exits.
func sandboxCommand(spec sandboxSpec, cmd []string) (argv []string, cleanup func()) {
	argv = sandboxArgv(spec, cmd)
	cleanup = func() {}
	if dir, procs, ok := newMemoryCgroup(); ok {
		argv = cgroupJoinArgv(procs, argv)
		cleanup = func() { removeCgroup(dir) }
	}
	return argv, cleanup
}

// removeCgroup deletes the cgroup, retrying briefly: rmdir fails with EBUSY
// while the kernel is still reaping the sandboxed process tree.
func removeCgroup(dir string) {
	for i := 0; i < 50; i++ {
		if err := os.Remove(dir); err == nil || os.IsNotExist(err) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// sandboxArgv wraps cmd to run inside bubblewrap: only /usr (plus usr-merge
// symlinks), a private /proc, /dev, /tmp and the spec's paths are visible, and
// every namespace is unshared so there is no network. When prlimit is present
// the call is also given CPU, file-size, fd and process ceilings.
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
		"--tmpfs", "/tmp",
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

// newMemoryCgroup creates a v1 memory cgroup capped at cgroupMemBytes. ok is
// false when v1 memory cgroups are unavailable, in which case callers proceed
// without a memory cap.
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

// cgroupJoinArgv prepends a shell that moves itself into the cgroup, then
// execs argv — so the process and all its descendants run under the cgroup.
func cgroupJoinArgv(procsFile string, argv []string) []string {
	script := "echo $$ > " + procsFile + `; exec "$@"`
	return append([]string{"sh", "-c", script, "sh"}, argv...)
}
