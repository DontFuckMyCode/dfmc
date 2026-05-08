package conversation

// manager_branches.go — branch lifecycle and undo. BranchCreate / Switch
// / List / Compare let a session fork mid-conversation so the user can
// explore an alternative without losing the main thread; UndoLast peels
// the last user/assistant pair off the active branch when the operator
// wants to retry. All operations take m.mu and operate purely in
// memory; persistence happens via SaveActive in manager_persist.go.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (m *Manager) BranchCreate(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return fmt.Errorf("no active conversation")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("branch name is required")
	}
	// Reject names that could escape the branch map key namespace or
	// confuse UIs that use the name as a path segment or display label.
	// Mirror validateConvID from store.go for consistency.
	if filepath.IsAbs(name) {
		return fmt.Errorf("invalid branch name %q: must be a relative name", name)
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("invalid branch name %q: must not contain path separators", name)
	}
	if name == "." || name == ".." || strings.Contains(name, "..") {
		return fmt.Errorf("invalid branch name %q: must not contain `..`", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("invalid branch name: contains control character U+%04X", r)
		}
	}
	if _, ok := m.active.Branches[name]; ok {
		return fmt.Errorf("branch already exists: %s", name)
	}
	current := m.active.Branches[m.active.Branch]
	copyMsgs := cloneMessages(current)
	m.active.Branches[name] = copyMsgs
	return nil
}

func (m *Manager) BranchSwitch(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return fmt.Errorf("no active conversation")
	}
	if _, ok := m.active.Branches[name]; !ok {
		return fmt.Errorf("branch not found: %s", name)
	}
	m.active.Branch = name
	return nil
}

func (m *Manager) BranchList() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil {
		return nil
	}
	out := make([]string, 0, len(m.active.Branches))
	for name := range m.active.Branches {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (m *Manager) BranchCompare(branchA, branchB string) (BranchComparison, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil {
		return BranchComparison{}, fmt.Errorf("no active conversation")
	}
	a := strings.TrimSpace(branchA)
	b := strings.TrimSpace(branchB)
	if a == "" || b == "" {
		return BranchComparison{}, fmt.Errorf("both branch names are required")
	}
	msgsA, ok := m.active.Branches[a]
	if !ok {
		return BranchComparison{}, fmt.Errorf("branch not found: %s", a)
	}
	msgsB, ok := m.active.Branches[b]
	if !ok {
		return BranchComparison{}, fmt.Errorf("branch not found: %s", b)
	}
	shared := sharedPrefixLen(msgsA, msgsB)
	return BranchComparison{
		BranchA:       a,
		BranchB:       b,
		MessagesA:     len(msgsA),
		MessagesB:     len(msgsB),
		SharedPrefixN: shared,
		OnlyA:         max(0, len(msgsA)-shared),
		OnlyB:         max(0, len(msgsB)-shared),
	}, nil
}

func (m *Manager) UndoLast() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return 0, fmt.Errorf("no active conversation")
	}
	msgs := m.active.Branches[m.active.Branch]
	if len(msgs) == 0 {
		return 0, nil
	}

	removed := 1
	trimTo := len(msgs) - 1
	if len(msgs) >= 2 {
		last := msgs[len(msgs)-1]
		prev := msgs[len(msgs)-2]
		if prev.Role == types.RoleUser && last.Role == types.RoleAssistant {
			removed = 2
			trimTo = len(msgs) - 2
		}
	}
	if trimTo < 0 {
		trimTo = 0
	}
	next := cloneMessages(msgs[:trimTo])
	m.active.Branches[m.active.Branch] = next
	return removed, nil
}

func sharedPrefixLen(a, b []types.Message) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i].Role != b[i].Role || a[i].Content != b[i].Content {
			return i
		}
	}
	return n
}
