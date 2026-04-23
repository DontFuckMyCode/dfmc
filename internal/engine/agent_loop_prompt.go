// agent_loop_prompt.go — system-prompt and meta-tool instruction builders
// for the native agent loop.
//
//   - buildNativeToolSystemPromptBundle: composes the project-aware system
//     prompt and folds the short native-mode tool-surface block into the
//     stable (cacheable) prefix so the instruction stack rides the
//     Anthropic prompt cache together with the base template.
//   - appendAutonomySystemSection: appends the autonomy preflight notice
//     (budget headroom, auto-resume hints) as a trailing system block.
//   - buildNativeMetaToolInstructions: renders the canonical "you have 4
//     meta tools..." bridge the model sees every turn. Single source of
//     truth for tool-choice policy in native mode.
//   - metaSpecsToDescriptors / metaToolNames: small adapters between the
//     tools.ToolSpec and provider.ToolDescriptor shapes.
//
// Extracted from agent_loop_native.go to keep the main loop file focused
// on control flow.

package engine

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// buildNativeToolSystemPromptBundle composes the standard system prompt
// (project brief, task style, tool-call policy) and folds the short
// native-mode tool surface block into the *stable* prefix so the whole
// instruction stack (≈40 extra tokens) is eligible for Anthropic prompt
// caching alongside the rest of the base template. Returns both the flat
// text (for providers that ignore caching) and the structured SystemBlocks.
func (e *Engine) buildNativeToolSystemPromptBundle(question string, chunks []types.ContextChunk, preflight *autonomyPreflight) (string, []provider.SystemBlock) {
	var bundle *promptlib.PromptBundle
	if e.Context != nil {
		bundle = e.Context.BuildSystemPromptBundle(
			e.ProjectRoot,
			question,
			chunks,
			e.ListTools(),
			e.promptRuntime(),
		)
	}
	bridge := strings.TrimSpace(buildNativeMetaToolInstructions(e.Tools.BackendSpecs()))
	if bridge == "" {
		text, blocks := bundleToSystemBlocks(bundle)
		return appendAutonomySystemSection(text, blocks, preflight)
	}
	bridgeText := "[DFMC native tool surface]\n" + bridge

	if bundle == nil || len(bundle.Sections) == 0 {
		composed := &promptlib.PromptBundle{Sections: []promptlib.PromptSection{
			{Label: "stable", Text: bridgeText, Cacheable: true},
		}}
		text, blocks := bundleToSystemBlocks(composed)
		return appendAutonomySystemSection(text, blocks, preflight)
	}

	sections := make([]promptlib.PromptSection, 0, len(bundle.Sections)+1)
	injected := false
	for _, s := range bundle.Sections {
		if !injected && s.Cacheable {
			s.Text = strings.TrimSpace(s.Text) + "\n\n" + bridgeText
			injected = true
		}
		sections = append(sections, s)
	}
	if !injected {
		// No cacheable section exists yet (template lacks the cache-break
		// marker). Prepend a stable section so the tool surface stays
		// cacheable on its own and the base prompt remains dynamic until a
		// template author adds a boundary.
		sections = append([]promptlib.PromptSection{
			{Label: "stable", Text: bridgeText, Cacheable: true},
		}, sections...)
	}
	text, blocks := bundleToSystemBlocks(&promptlib.PromptBundle{Sections: sections})
	return appendAutonomySystemSection(text, blocks, preflight)
}

func appendAutonomySystemSection(text string, blocks []provider.SystemBlock, preflight *autonomyPreflight) (string, []provider.SystemBlock) {
	block := buildAutonomySystemSection(preflight)
	if block == nil {
		return text, blocks
	}
	if strings.TrimSpace(text) == "" {
		text = block.Text
	} else {
		text = strings.TrimSpace(text) + "\n\n" + block.Text
	}
	blocks = append(blocks, *block)
	return text, blocks
}

func buildNativeMetaToolInstructions(backend []tools.ToolSpec) string {
	lines := []string{
		"You have 4 meta tools that proxy to a richer backend registry:",
		"  - tool_search(query, limit?) — discover backend tools by topic",
		"  - tool_help(name)            — fetch full schema/usage for one tool",
		"  - tool_call(name, args)      — execute a single backend tool",
		"  - tool_batch_call(calls[])   — execute several backend tools in one round-trip",
		"Discover before invoking. Cite evidence by file/line. Never dump raw tool output to the user.",
		"Operate autonomously: keep going until the task is complete or you are truly blocked; do not stop after a single tool result when more reads, edits, verification, or research are still required.",
		"For multi-step work, keep todo_write current early. When the request clearly decomposes, prefer orchestrate or delegate_task fan-out instead of forcing every subtask through one serial loop.",
		"When choosing tools: grep_codebase for content discovery, glob for file-shape discovery, read_file for exact file slices, ast_query/find_symbol for structured symbol lookup, edit_file for small exact edits, apply_patch for multi-hunk edits, write_file for new files or full rewrites, run_command only for build/test/lint or when no native tool exists.",
		"Before ANY mutation of an existing file, read it first. Then prefer edit_file for one focused replacement, apply_patch for several coordinated hunks, and write_file only when replacing most of the file or creating a new one.",
		"Use tool_batch_call for independent read-only fan-out (for example several read_file / grep_codebase / glob calls). Do not batch dependent steps, and do not nest tool_call inside tool_batch_call unless you are correcting an existing malformed shape.",
		"Common parameter shapes: read_file(path,line_start,line_end), grep_codebase(pattern,path?,max_results?), glob(pattern,path?), find_symbol(name,kind?,path?), ast_query(path,kind?,name_contains?), run_command(command,args,dir?,timeout_ms?).",
		"run_command is NOT a shell. command is argv[0] only; put the rest in args. Never send &&, ||, ;, |, >, cd ..., $(...), or backticks.",
		"If a tool fails, do not blindly retry the identical call. Read the error, fix the arg shape or choose a narrower tool. Missing-field errors usually mean you used the wrong parameter names or the wrong tool for the job.",
		"Keep reads narrow: prefer focused patterns, exact symbols, and bounded line ranges over scanning whole files or broad recursive sweeps. Large broad reads should be the exception, not the default.",
	}
	if len(backend) > 0 {
		preview := backend
		if len(preview) > 6 {
			preview = preview[:6]
		}
		names := make([]string, 0, len(preview))
		for _, s := range preview {
			names = append(names, s.Name)
		}
		hint := "Backend registry includes: " + strings.Join(names, ", ")
		if len(backend) > len(preview) {
			hint += fmt.Sprintf(" (+%d more — use tool_search to discover)", len(backend)-len(preview))
		}
		lines = append(lines, hint)
	}
	return strings.Join(lines, "\n")
}

// metaSpecsToDescriptors converts ToolSpecs into the provider-agnostic
// ToolDescriptor shape that providers serialize into Anthropic's tools[] or
// OpenAI's tools[].function entries.
func metaSpecsToDescriptors(specs []tools.ToolSpec) []provider.ToolDescriptor {
	out := make([]provider.ToolDescriptor, 0, len(specs))
	for _, s := range specs {
		out = append(out, provider.ToolDescriptor{
			Name:        s.Name,
			Description: s.Summary,
			InputSchema: s.JSONSchema(),
		})
	}
	return out
}

func metaToolNames(descs []provider.ToolDescriptor) []string {
	names := make([]string, 0, len(descs))
	for _, d := range descs {
		names = append(names, d.Name)
	}
	return names
}
