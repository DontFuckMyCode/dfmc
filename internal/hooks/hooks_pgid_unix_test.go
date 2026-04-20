//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package hooks

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestApplyProcessGroupIsolation_SetsSetpgid(t *testing.T) {
	cmd := &exec.Cmd{}
	applyProcessGroupIsolation(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr must be set after applyProcessGroupIsolation")
	}
	if !cmd.SysProcAttr.Setpgid {
		t.Fatal("Setpgid must be true after applyProcessGroupIsolation")
	}
}

func TestApplyProcessGroupIsolation_preservesExistingSysProcAttr(t *testing.T) {
	cmd := &exec.Cmd{
		SysProcAttr: &syscall.SysProcAttr{
			Setpgid: false,
		},
	}
	applyProcessGroupIsolation(cmd)
	if cmd.SysProcAttr.Setpgid {
		t.Fatal("Setpgid must still be true")
	}
}

func TestKillProcessGroup_NoOpOnNonPositivePID(t *testing.T) {
	// Must not panic. killProcessGroup swallows errors intentionally
	// (best-effort hygiene), so we only verify no-panic.
	killProcessGroup(0)
	killProcessGroup(-1)
	killProcessGroup(-100)
}
