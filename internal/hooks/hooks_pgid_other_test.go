//go:build !windows && !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package hooks

import (
	"os/exec"
	"testing"
)

// TestApplyProcessGroupIsolation_StubNoOp verifies the fallback platform stub
// does not panic and does not modify the command.
func TestApplyProcessGroupIsolation_StubNoOp(t *testing.T) {
	cmd := &exec.Cmd{}
	applyProcessGroupIsolation(cmd)
	// Must not panic; zero-value Cmd is unchanged.
}

// TestKillProcessGroup_StubNoOp verifies the fallback stub for
// platforms without POSIX process groups or Windows taskkill support
// silently ignores the call without panicking.
func TestKillProcessGroup_StubNoOp(t *testing.T) {
	killProcessGroup(0)
	killProcessGroup(-1)
	killProcessGroup(12345)
}
