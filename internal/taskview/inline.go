package taskview

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

const UnknownSubcommandHelp = "tasks: unknown subcommand. Try: /tasks [list|tree|show <id>|roots|clear|open|close]"

func List(store *taskstore.Store) string {
	all, err := store.ListTasks(taskstore.ListOptions{})
	if err != nil {
		return "error: " + err.Error()
	}
	return RenderList(all)
}

func Roots(store *taskstore.Store) string {
	all, err := store.ListTasks(taskstore.ListOptions{})
	if err != nil {
		return "error: " + err.Error()
	}
	var roots []*supervisor.Task
	for _, t := range all {
		if t.ParentID == "" {
			roots = append(roots, t)
		}
	}
	return RenderList(roots)
}

func Tree(store *taskstore.Store, rootID string) string {
	all, err := store.ListTasks(taskstore.ListOptions{})
	if err != nil {
		return "error: " + err.Error()
	}
	var roots []*supervisor.Task
	if rootID != "" {
		for _, t := range all {
			if t.ID == rootID {
				roots = []*supervisor.Task{t}
				break
			}
		}
		if roots == nil {
			return "task not found: " + rootID
		}
	} else {
		for _, t := range all {
			if t.ParentID == "" {
				roots = append(roots, t)
			}
		}
	}
	if len(roots) == 0 {
		return "(no tasks)"
	}
	var b strings.Builder
	for i, root := range roots {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(renderNodeDepth(root, 0))
		b.WriteString("\n")
		addChildren(&b, all, root.ID, 1)
	}
	return b.String()
}

func Detail(store *taskstore.Store, id string) string {
	t, err := store.LoadTask(id)
	if err != nil {
		return "load error: " + err.Error()
	}
	if t == nil {
		return "task not found: " + id
	}
	return FormatDetail(t)
}

func ClearNonDrive(store *taskstore.Store) string {
	all, err := store.ListTasks(taskstore.ListOptions{})
	if err != nil {
		return "/tasks clear failed: " + err.Error()
	}
	if len(all) == 0 {
		return "/tasks clear: store is already empty."
	}
	deleted := 0
	skipped := 0
	var firstErr error
	for _, t := range all {
		if t.RunID != "" {
			skipped++
			continue
		}
		if delErr := store.DeleteTask(t.ID); delErr != nil {
			if firstErr == nil {
				firstErr = delErr
			}
			continue
		}
		deleted++
	}
	out := fmt.Sprintf("▸ Cleared %d task(s) from the store.", deleted)
	if skipped > 0 {
		out += fmt.Sprintf(" %d drive-owned task(s) kept (use /drive stop <id> to cancel a run).", skipped)
	}
	if firstErr != nil {
		out += "\n   First error: " + firstErr.Error()
	}
	return out
}

func RenderList(tasks []*supervisor.Task) string {
	if len(tasks) == 0 {
		return "(no tasks)"
	}
	var b strings.Builder
	for _, t := range tasks {
		b.WriteString(RenderNode(t))
		b.WriteString("\n")
	}
	return b.String()
}

func RenderNode(t *supervisor.Task) string {
	return fmt.Sprintf("%s %s", StateIcon(t.State), t.Title)
}

func FormatDetail(t *supervisor.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "▸ %s  [%s]\n", t.Title, t.State)
	if t.Detail != "" {
		fmt.Fprintf(&b, "  detail:   %s\n", t.Detail)
	}
	if t.ParentID != "" {
		fmt.Fprintf(&b, "  parent:   %s\n", t.ParentID)
	}
	if len(t.DependsOn) > 0 {
		fmt.Fprintf(&b, "  depends:  %s\n", strings.Join(t.DependsOn, ", "))
	}
	if t.BlockedReason != "" {
		fmt.Fprintf(&b, "  blocked:  %s\n", t.BlockedReason)
	}
	if t.WorkerClass != "" {
		fmt.Fprintf(&b, "  worker:   %s\n", t.WorkerClass)
	}
	if len(t.Labels) > 0 {
		fmt.Fprintf(&b, "  labels:   %s\n", strings.Join(t.Labels, ", "))
	}
	if t.Verification != "" {
		fmt.Fprintf(&b, "  verify:   %s\n", t.Verification)
	}
	if t.Confidence > 0 {
		fmt.Fprintf(&b, "  conf:     %.0f%%\n", t.Confidence*100)
	}
	if t.Summary != "" {
		fmt.Fprintf(&b, "  summary:  %s\n", t.Summary)
	}
	if t.Error != "" {
		fmt.Fprintf(&b, "  error:    %s\n", t.Error)
	}
	if !t.StartedAt.IsZero() {
		fmt.Fprintf(&b, "  started:  %s\n", t.StartedAt.Format("2006-01-02 15:04:05"))
	}
	if !t.EndedAt.IsZero() {
		fmt.Fprintf(&b, "  ended:    %s\n", t.EndedAt.Format("2006-01-02 15:04:05"))
	}
	return strings.TrimRight(b.String(), "\n")
}

func StateIcon(state supervisor.TaskState) string {
	switch state {
	case supervisor.TaskDone:
		return "✓"
	case supervisor.TaskRunning:
		return "…"
	case supervisor.TaskBlocked:
		return "✗"
	case supervisor.TaskSkipped:
		return "⤳"
	case supervisor.TaskWaiting:
		return "⧖"
	case supervisor.TaskExternalReview:
		return "⚠"
	default:
		return "○"
	}
}

func addChildren(b *strings.Builder, all []*supervisor.Task, parentID string, depth int) {
	for _, t := range all {
		if t.ParentID == parentID {
			b.WriteString(renderNodeDepth(t, depth))
			b.WriteString("\n")
			addChildren(b, all, t.ID, depth+1)
		}
	}
}

func renderNodeDepth(t *supervisor.Task, depth int) string {
	indent := strings.Repeat("  ", depth)
	return fmt.Sprintf("%s%s %s", indent, StateIcon(t.State), t.Title)
}
