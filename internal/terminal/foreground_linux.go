//go:build linux

package terminal

import (
	"bytes"
	"os"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// foregroundProcesses lists the job running in the pane, with the PIDs a shell inside the sandbox would see.
// It is empty while the shell itself is in the foreground.
func foregroundProcesses(master *os.File) []paneProcess {
	raw, err := master.SyscallConn()
	if err != nil {
		return nil
	}
	pgrp, sid := -1, -1
	_ = raw.Control(func(fd uintptr) {
		pgrp, _ = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
		sid, _ = unix.IoctlGetInt(int(fd), unix.TIOCGSID)
	})
	// The shell leads the session, so its own group in the foreground means it is at the prompt.
	if pgrp <= 0 || pgrp == sid {
		return nil
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var procs []paneProcess
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		name, group, ok := readStat(pid)
		if !ok || group != pgrp {
			continue
		}
		if inner := sandboxPid(pid); inner > 0 {
			procs = append(procs, paneProcess{Pid: inner, Name: name})
		}
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].Pid < procs[j].Pid })
	return procs
}

// readStat returns a process's command name and process group from /proc/<pid>/stat.
func readStat(pid int) (string, int, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", 0, false
	}
	// comm may contain spaces or parens, so split around its last ')'.
	open, end := bytes.IndexByte(data, '('), bytes.LastIndexByte(data, ')')
	if open < 0 || end < open {
		return "", 0, false
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 3 {
		return "", 0, false
	}
	group, err := strconv.Atoi(fields[2])
	if err != nil {
		return "", 0, false
	}
	return string(data[open+1 : end]), group, true
}

// sandboxPid is the PID in the process's innermost namespace, the one its sandbox's shell uses.
func sandboxPid(pid int) int {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "NSpid:"); ok {
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				return 0
			}
			inner, _ := strconv.Atoi(fields[len(fields)-1])
			return inner
		}
	}
	return 0
}
