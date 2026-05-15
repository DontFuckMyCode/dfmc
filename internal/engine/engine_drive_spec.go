// engine_drive_spec.go — bridge between the spec_to_todo tool and
// the drive package's preset-Todo path. Lives in the engine package
// (not CLI / web / MCP) so every surface that wants to start a Drive
// run from a markdown spec calls the same helper — uniform behaviour
// across `dfmc drive --from-spec`, POST /api/v1/drive with
// from_spec=..., and the dfmc_drive_start MCP tool.

package engine

import (
	"context"
	"fmt"

	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// TodosFromSpecFile reads the given markdown spec, runs the
// spec_to_todo tool against it (with the optional section / include-
// done filters), and converts the result into drive.Todo records
// suitable for handing into Driver.RunPrepared as a preset plan.
//
// Returns (todos, dropped, err). `dropped` counts items the converter
// skipped because they had no title — surfaces fail-soft so callers
// can warn but still proceed when partial coverage is acceptable.
//
// `specPath` is interpreted relative to e.ProjectRoot; absolute
// paths must still resolve under the project root (spec_to_todo
// runs the same EnsureWithinRoot guard as every other read tool).
//
// Lifecycle bypass: this helper constructs and calls SpecToTodoTool
// directly instead of routing through CallToolFromSource/
// executeToolWithLifecycle. That bypass is deliberate and documented
// in CLAUDE.md as one of the two allowed exceptions — TodosFromSpecFile
// is invoked from contexts where the engine may not yet be in
// StateReady (CLI init paths, partial-engine tests building Engine{}
// without Init), and spec_to_todo is a pure read-only markdown parser
// with its own EnsureWithinRoot guard, so the hook/approval surface
// adds no safety value. Any future bypass needs a comparable
// justification.
func (e *Engine) TodosFromSpecFile(ctx context.Context, specPath, section string, includeDone bool) ([]drive.Todo, int, error) {
	if e == nil {
		return nil, 0, ErrEngineNil
	}
	tool := tools.NewSpecToTodoTool()
	params := map[string]any{"path": specPath}
	if section != "" {
		params["section"] = section
	}
	if includeDone {
		params["include_done"] = true
	}
	res, err := tool.Execute(ctx, tools.Request{
		ProjectRoot: e.ProjectRoot,
		Params:      params,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("spec_to_todo: %w", err)
	}
	rawItems, _ := res.Data["todos"].([]map[string]any)
	todos, dropped := drive.TodosFromSpec(rawItems)
	return todos, dropped, nil
}
