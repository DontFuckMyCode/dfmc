package engine

// subagent.go - bounded sub-agent runner used by the delegate_task tool.
//
// A sub-agent runs its own provider-native tool loop with a fresh message
// history and its own step/token budget. It does NOT share parked state with
// the parent: any parked state saved during its run is cleared before the
// parent's (saved aside) state is restored, so a model can delegate
// recursively without stomping on its own workspace.

import (
	"context"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// RunSubagent implements tools.SubagentRunner. The delegate_task tool calls
// this to execute a scoped sub-task with its own fresh context and budget.
func (e *Engine) RunSubagent(ctx context.Context, req tools.SubagentRequest) (tools.SubagentResult, error) {
	return e.runSubagentProfiles(ctx, req, nil)
}

// buildSubagentPrompt stitches role and allowed-tool hints onto the raw task
// so the sub-agent sees them as part of its user-facing question. Keeping
// these in the user prompt (rather than inventing a parallel system-prompt
// variant) means behavior degrades gracefully on providers we haven't
// specially tuned for.
func buildSubagentPrompt(req tools.SubagentRequest, skillTexts []string) string {
	var b strings.Builder
	if len(skillTexts) > 0 {
		for _, text := range skillTexts {
			b.WriteString(text)
			b.WriteString("\n\n")
		}
	}
	role := strings.TrimSpace(req.Role)
	if role != "" {
		b.WriteString("You are acting as a ")
		b.WriteString(role)
		b.WriteString(" sub-agent spawned by the main session. Focus narrowly on the task and report back a concise summary.\n\n")
	} else {
		b.WriteString("You are a bounded sub-agent. Complete the task and return a concise summary.\n\n")
	}
	if len(req.AllowedTools) > 0 {
		b.WriteString("Preferred tools: ")
		b.WriteString(strings.Join(req.AllowedTools, ", "))
		b.WriteString(". Avoid tools outside this list unless essential.\n\n")
	}
	b.WriteString("Work autonomously until the scoped task is genuinely complete. If the task clearly decomposes, prefer orchestrate or delegate_task fan-out instead of serializing every survey step yourself.\n\n")
	b.WriteString("Task:\n")
	b.WriteString(strings.TrimSpace(req.Task))
	return b.String()
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// assert tools.SubagentRunner at compile time.
var _ tools.SubagentRunner = (*Engine)(nil)

// ensure provider.Message / types import usage don't become unused in edits.
var _ = provider.Message{}
var _ = types.RoleUser
