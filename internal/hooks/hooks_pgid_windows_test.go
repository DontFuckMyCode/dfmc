//go:build windows

package hooks

import (
	"os"
	"os/exec"
	"slices"
	"testing"
)

// TestApplyProcessGroupIsolation_IsNoOpOnWindows verifies the Windows stub
// does not modify the command. On Windows the cmd.exe parent already has a
// console and our taskkill /T cleanup handles the common cases.
func TestApplyProcessGroupIsolation_IsNoOpOnWindows(t *testing.T) {
	cmd := &exec.Cmd{}
	applyProcessGroupIsolation(cmd)
	// Must not panic and must not have side effects.
}

// TestKillProcessGroup_NoOpOnNonPositivePID verifies killProcessGroup
// silently ignores non-positive PIDs without panicking.
func TestKillProcessGroup_NoOpOnNonPositivePID(t *testing.T) {
	killProcessGroup(0)
	killProcessGroup(-1)
	killProcessGroup(-100)
}

// TestDescendantPIDs_ReturnsAtLeastRoot verifies that when the process
// table is empty or the target has no children, descendantPids returns
// at least the root PID so the caller still attempts to terminate it.
func TestDescendantPIDs_ReturnsAtLeastRoot(t *testing.T) {
	result := descendantPIDs(uint32(os.Getpid()))
	if len(result) == 0 {
		t.Fatal("descendantPIDs must return at least the root PID")
	}
	if !slices.Contains(result, uint32(os.Getpid())) {
		t.Fatalf("descendantPIDs result must contain root PID %d, got %v", os.Getpid(), result)
	}
}

// TestTerminateProcess_NoOpOnZeroPID verifies terminateProcess is safe
// to call with PID 0 — it must not panic and must not attempt to open
// an invalid handle.
func TestTerminateProcess_NoOpOnZeroPID(t *testing.T) {
	terminateProcess(0)
}
