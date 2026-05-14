package tui

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

func (m Model) statsTaskTreeLines() []string {
	if m.eng == nil || m.eng.Tools == nil || m.eng.Tools.TaskStore() == nil {
		return nil
	}
	storeTasks, _ := m.eng.Tools.TaskStore().ListTasks(taskstore.ListOptions{})
	if len(storeTasks) == 0 {
		return nil
	}

	children := make(map[string][]*supervisor.Task)
	var roots []*supervisor.Task
	for _, t := range storeTasks {
		if t.ParentID == "" {
			roots = append(roots, t)
		} else {
			children[t.ParentID] = append(children[t.ParentID], t)
		}
	}

	var lines []string
	var buildTree func(t *supervisor.Task, indent int, isLast bool)
	buildTree = func(t *supervisor.Task, indent int, isLast bool) {
		prefix := ""
		if indent > 0 {
			treeChar := "+-"
			if isLast {
				treeChar = "`-"
			}
			prefix = strings.Repeat("  ", indent-1) + treeChar + " "
		}
		title := t.Title
		if title == "" {
			title = t.Detail
		}
		lines = append(lines, fmt.Sprintf("%s[%s] %s  %s", prefix, t.State, t.ID, title))
		kids := children[t.ID]
		for i, child := range kids {
			buildTree(child, indent+1, i == len(kids)-1)
		}
	}
	for i, root := range roots {
		buildTree(root, 0, i == len(roots)-1)
	}
	return lines
}
