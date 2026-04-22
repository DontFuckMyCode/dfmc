//go:build windows

// Process-group isolation for hook subprocess trees on Windows.
//
// Windows doesn't have POSIX process groups, but we can still do
// best-effort tree cleanup without invoking an external shell command.
// We snapshot the process table, walk descendants rooted at the hook
// shell's PID, then terminate children before the parent. This avoids
// spawning `taskkill.exe`, which security scanners often flag as a
// command-injection sink even though the old call only passed a numeric
// PID as argv.

package hooks

import (
	"errors"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// applyProcessGroupIsolation is a no-op on Windows. The cmd.exe parent
// already inherits a console, and our taskkill /T cleanup is enough
// for the common cases.
func applyProcessGroupIsolation(_ *exec.Cmd) {}

// killProcessGroup terminates the named process and every descendant we
// can find in the live process snapshot. Errors are ignored — this is
// best-effort hygiene after a timeout/cancel and must never block hook
// dispatch completion.
func killProcessGroup(pid int) {
	if pid <= 0 {
		return
	}

	for _, target := range descendantPIDs(uint32(pid)) {
		terminateProcess(target)
	}
}

func descendantPIDs(root uint32) []uint32 {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return []uint32{root}
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()

	children := map[uint32][]uint32{}
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return []uint32{root}
	}
	for {
		children[entry.ParentProcessID] = append(children[entry.ParentProcessID], entry.ProcessID)
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return []uint32{root}
		}
	}

	seen := map[uint32]struct{}{}
	order := make([]uint32, 0, 8)
	var walk func(uint32)
	walk = func(pid uint32) {
		if pid == 0 {
			return
		}
		if _, ok := seen[pid]; ok {
			return
		}
		seen[pid] = struct{}{}
		for _, child := range children[pid] {
			walk(child)
		}
		// Post-order so children are terminated before the root shell.
		order = append(order, pid)
	}
	walk(root)
	if len(order) == 0 {
		return []uint32{root}
	}
	return order
}

func terminateProcess(pid uint32) {
	if pid == 0 {
		return
	}
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	_ = windows.TerminateProcess(handle, 1)
}
