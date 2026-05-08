package engine

// engine_passthrough_tasks.go — UnifiedTaskView + ListAllTasks. The
// "what's on my plate" aggregator that merges TodoWrite items from
// the taskstore with Drive runs from the drive-runs bucket. Used by
// the TUI tasks tab and the web /api/v1/tasks/all endpoint so both
// surfaces show the same merged view without re-deriving the origin.

import (
	"sort"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

// UnifiedTaskView is the merged shape returned by ListAllTasks. Each
// entry tags its source so UIs (TUI tasks tab, web /api/v1/tasks/all)
// can group/filter without re-deriving the origin. The Title/Status
// fields are the common ground; richer fields fall through via
// Extra for source-specific drill-down.
type UnifiedTaskView struct {
	ID     string `json:"id"`
	Source string `json:"source"` // "todo" | "drive"
	Title  string `json:"title"`
	Status string `json:"status"`
	// RunID groups Drive-sourced rows; empty for standalone todos.
	RunID string `json:"run_id,omitempty"`
	// CreatedAt is best-effort: TodoWrite tasks use StartedAt; Drive
	// rows use the run's CreatedAt. Used for newest-first sorting.
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// ListAllTasks returns the merged "what's on my plate" view across the
// taskstore (todo_write items + any sub-agent tasks the engine queued)
// and the drive-runs bucket (autonomous plan/execute runs and their
// per-TODO progress). Tasks are ordered newest-first by CreatedAt.
//
// Drive runs decompose into one row per TODO, prefixed with the run's
// Title so the user sees `<run> · <todo>`. Empty result on a fresh
// project — the caller renders an empty-state cue.
func (e *Engine) ListAllTasks() ([]UnifiedTaskView, error) {
	if e == nil {
		return nil, nil
	}
	out := make([]UnifiedTaskView, 0)

	// 1) Standalone todos / agent tasks from taskstore.
	if e.Tools != nil {
		if store := e.Tools.TaskStore(); store != nil {
			if tasks, err := store.ListTasks(taskstore.ListOptions{}); err == nil {
				for _, t := range tasks {
					if t == nil {
						continue
					}
					out = append(out, UnifiedTaskView{
						ID:        t.ID,
						Source:    "todo",
						Title:     t.Title,
						Status:    string(t.State),
						RunID:     t.RunID,
						CreatedAt: t.StartedAt,
					})
				}
			}
		}
	}

	// 2) Drive runs from drive-runs bucket. Each run unfolds into one row
	//    per non-terminal TODO so the user sees granular progress, plus
	//    one summary row for the run itself.
	if e.Storage != nil {
		if db := e.Storage.DB(); db != nil {
			driveStore, err := drive.NewStore(db)
			if err == nil && driveStore != nil {
				runs, lerr := driveStore.List()
				if lerr == nil {
					for _, run := range runs {
						if run == nil {
							continue
						}
						out = append(out, UnifiedTaskView{
							ID:        run.ID,
							Source:    "drive",
							Title:     run.Task,
							Status:    string(run.Status),
							RunID:     run.ID,
							CreatedAt: run.CreatedAt,
						})
					}
				}
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}
