//go:build windows

// Process-group isolation for hook subprocess trees on Windows.
//
// Windows doesn't have POSIX process groups, but it has Job Objects
// for the equivalent semantics — assign processes to a job, then call
// TerminateJobObject to kill them all at once. Wiring that up requires
// the golang.org/x/sys/windows API surface and a non-trivial amount of
// CreateJobObject/SetInformationJobObject scaffolding.
//
// For now we settle for `taskkill /T /PID <pid> /F`, which terminates
// the named process and any process tree it spawned. This handles the
// "hook with `&` background child" case adequately and avoids pulling
// in a new dependency. Document this as a known limitation: hooks that
// spawn detached processes (start /B, win32 service starters) may
// still leak children.

package hooks

import (
	"os/exec"
	"strconv"
)

// applyProcessGroupIsolation is a no-op on Windows. The cmd.exe parent
// already inherits a console, and our taskkill /T cleanup is enough
// for the common cases.
func applyProcessGroupIsolation(_ *exec.Cmd) {}

// killProcessGroup invokes taskkill to terminate the named process and
// every child it spawned (/T = tree, /F = force). Errors are ignored —
// best-effort cleanup, the dispatcher has already moved on.
func killProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
