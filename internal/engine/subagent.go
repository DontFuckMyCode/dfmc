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
	"sort"
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

type subagentPromptEnvironment struct {
	ProjectRoot      string
	Provider         string
	Model            string
	MaxSteps         int
	BackendToolCount int
	BackendToolNames []string
}

// buildSubagentPrompt stitches role and allowed-tool hints onto the raw task
// so the sub-agent sees them as part of its user-facing question. Keeping
// these in the user prompt (rather than inventing a parallel system-prompt
// variant) means behavior degrades gracefully on providers we haven't
// specially tuned for.
func buildSubagentPrompt(req tools.SubagentRequest, skillTexts []string, env subagentPromptEnvironment) string {
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
	if env.ProjectRoot != "" || env.Provider != "" || env.Model != "" || env.MaxSteps > 0 || env.BackendToolCount > 0 {
		b.WriteString("Runtime context:\n")
		if env.ProjectRoot != "" {
			b.WriteString("- project_root: ")
			b.WriteString(env.ProjectRoot)
			b.WriteString("\n")
		}
		if env.Provider != "" || env.Model != "" {
			b.WriteString("- provider/model: ")
			if env.Provider != "" {
				b.WriteString(env.Provider)
			}
			if env.Model != "" {
				if env.Provider != "" {
					b.WriteString(" / ")
				}
				b.WriteString(env.Model)
			}
			b.WriteString("\n")
		}
		if env.MaxSteps > 0 {
			b.WriteString("- max_tool_steps: ")
			b.WriteString(itoaInt(env.MaxSteps))
			b.WriteString("\n")
		}
		if env.BackendToolCount > 0 {
			b.WriteString("- backend_tools: ")
			b.WriteString(itoaInt(env.BackendToolCount))
			if len(env.BackendToolNames) > 0 {
				b.WriteString(" available through tool_call/tool_batch_call; sample: ")
				b.WriteString(strings.Join(env.BackendToolNames, ", "))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(req.AllowedTools) > 0 {
		b.WriteString("Allowed tools for this delegation: ")
		b.WriteString(strings.Join(req.AllowedTools, ", "))
		b.WriteString(". Treat this as the hard scoped tool set; ask for a narrower follow-up if you need something outside it.\n\n")
	}
	b.WriteString("Work autonomously until the scoped task is genuinely complete. If the task clearly decomposes, prefer orchestrate or delegate_task fan-out instead of serializing every survey step yourself.\n\n")
	b.WriteString("Return shape — what the parent session needs from you:\n")
	b.WriteString("- A direct answer to the scoped task (one paragraph, not a recap of every step).\n")
	b.WriteString("- Concrete evidence: file:line citations, key snippets, or exact tool output excerpts behind your claim.\n")
	b.WriteString("- Open questions or assumptions you had to make, listed separately so the parent can decide whether to probe further.\n")
	b.WriteString("- Skip narration of intermediate tool calls — the parent already sees the trace; your job is the synthesis.\n\n")
	b.WriteString("Task:\n")
	b.WriteString(strings.TrimSpace(req.Task))
	return b.String()
}

func subagentPromptToolSample(specs []tools.ToolSpec, limit int) []string {
	if limit <= 0 || len(specs) == 0 {
		return nil
	}
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		name := strings.TrimSpace(spec.Name)
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > limit {
		names = names[:limit]
	}
	return names
}

func (e *Engine) subagentConcurrencyLimit() int {
	return e.parallelBatchSize()
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
