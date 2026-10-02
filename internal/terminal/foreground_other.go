//go:build !linux

package terminal

import "os"

// Dev fallback: there is no /proc to map the foreground job to PIDs.
func foregroundProcesses(*os.File) []paneProcess { return nil }
