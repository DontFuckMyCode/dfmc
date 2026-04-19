// Drive event names.
//
// All published through engine.EventBus; subscribers in the TUI/web
// renderer key off these strings. The names follow the existing
// `noun:verb` convention used by agent: and provider: events so a
// regex subscriber for `^drive:` catches the whole namespace.
//
// New events here MUST also be documented in CLAUDE.md so frontend
// authors don't have to read the source to learn about them.

package drive

const (
	EventRunStart    = "drive:run:start"    // {run_id, task}
	EventPlanStart   = "drive:plan:start"   // {run_id, model}
	EventPlanDone    = "drive:plan:done"    // {run_id, todo_count, todos: [{id,title,deps}]}
	EventPlanAugment = "drive:plan:augment" // {run_id, added, todos: [{id,title,deps,origin,kind}]}
	EventPlanFailed  = "drive:plan:failed"  // {run_id, error}
	EventTodoStart   = "drive:todo:start"   // {run_id, todo_id, title, attempt}
	EventTodoDone    = "drive:todo:done"    // {run_id, todo_id, brief, duration_ms, tool_calls}
	EventTodoBlocked = "drive:todo:blocked" // {run_id, todo_id, error, attempts}
	EventTodoSkipped = "drive:todo:skipped" // {run_id, todo_id, reason}
	EventTodoRetry   = "drive:todo:retry"   // {run_id, todo_id, attempt, last_error}
	EventRunWarning  = "drive:run:warning"  // {run_id, error, status}
	EventRunDone     = "drive:run:done"     // {run_id, status, done, blocked, skipped, duration_ms}
	EventRunStopped  = "drive:run:stopped"  // {run_id, reason}
	EventRunFailed   = "drive:run:failed"   // {run_id, reason}
)
