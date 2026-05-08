package drive

// spec_ingest.go — convert the map-shaped output of the spec_to_todo
// tool into drive.Todo records. Lives in the drive package (not the
// tools package) because Todo is a drive type and this function is
// the one that decides how spec semantics map onto Drive scheduling
// fields. Callers (CLI --from-spec, future HTTP/MCP) feed the raw
// items array straight from spec_to_todo's `data.todos` slice.
//
// The conversion is permissive: missing fields fall back to sensible
// defaults so a hand-written items list (not strictly produced by
// spec_to_todo) still produces a runnable plan. Each item must at
// least carry a non-empty `title` — without one, Drive cannot render
// progress lines.

import (
	"fmt"
	"strings"
)

// TodosFromSpec converts the items emitted by the spec_to_todo tool
// into drive.Todo records. The input map shape mirrors what
// internal/tools/spec_to_todo writes into Result.Data["todos"]:
//
//	{
//	  "id": "phase-1-0",
//	  "title": "...",
//	  "detail": "...",
//	  "kind": "code",
//	  "worker_class": "coder",
//	  "provider_tag": "code",
//	  "read_only": false,
//	  "status": "pending"|"done",
//	  "source_section": "phase-1",
//	  "source_line": 7
//	}
//
// Items missing `title` are dropped (with their index in the returned
// `dropped` count). The returned Todos preserve the input order.
func TodosFromSpec(items []map[string]any) (todos []Todo, dropped int) {
	todos = make([]Todo, 0, len(items))
	for _, item := range items {
		title := strings.TrimSpace(asMapString(item, "title"))
		if title == "" {
			dropped++
			continue
		}
		id := strings.TrimSpace(asMapString(item, "id"))
		if id == "" {
			id = fmt.Sprintf("spec-%d", len(todos))
		}
		status := TodoStatus(strings.ToLower(strings.TrimSpace(asMapString(item, "status"))))
		switch status {
		case TodoDone, TodoSkipped:
			// keep terminal status — preset spec marks completed work.
		default:
			status = TodoPending
		}
		td := Todo{
			ID:          id,
			Title:       title,
			Detail:      asMapString(item, "detail"),
			Kind:        asMapString(item, "kind"),
			WorkerClass: asMapString(item, "worker_class"),
			ProviderTag: asMapString(item, "provider_tag"),
			ReadOnly:    asMapBool(item, "read_only"),
			Origin:      "spec",
			Status:      status,
		}
		todos = append(todos, td)
	}
	return todos, dropped
}

func asMapString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asMapBool(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	v, ok := m[key]
	if !ok {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}
