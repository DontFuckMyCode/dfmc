//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

// Process-group isolation for hook subprocess trees on Unix-like
// systems. Setpgid puts the hook's shell into its own process group;
// killProcessGroup then sends SIGKILL to the whole group so background
// children don't outlive a timed-out hook.

package hooks

import (
	"os/exec"
	"syscall"
)

// applyProcessGroupIsolation sets SysProcAttr.Setpgid so the hook gets
// its own process group keyed on the shell's PID. Without this, an
// `sh -c "thing &"` orphan would re-parent to init and live forever
// after the parent timeout.
func applyProcessGroupIsolation(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup sends SIGKILL to the whole process group rooted at
// pid. The negative-PID convention is the POSIX way to address a group
// (kill(-pgid, sig)). Errors are silently swallowed — the hook is
// already done from the dispatcher's perspective and the only reason
// we're killing is hygiene.
func killProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
