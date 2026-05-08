package tui

// patch_view_annotate.go — transcript annotation methods that the
// Patch panel relies on. Each chatLine in the chat tab carries
// PatchFiles / PatchHunks / IsLatestPatch / ToolCalls / ToolNames /
// ToolFailures fields; these methods populate those after a turn
// completes so the chip strip and Patch panel cross-reference stay
// consistent. matchAssistantConversationMessage joins the rendered
// transcript line back to the engine.Conversation message it came
// from so the per-turn tool roster stays accurate even after
// formatting / ANSI strips. Sibling to patch_view.go which owns the
// panel state, action menu, navigation, and render helpers.

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (m Model) chatPatchSummary(item chatLine) string {
	if len(item.PatchFiles) == 0 && item.PatchHunks == 0 && item.ToolCalls == 0 {
		return ""
	}
	parts := make([]string, 0, 6)
	if len(item.PatchFiles) > 0 {
		parts = append(parts, fmt.Sprintf("patch: %s", strings.Join(item.PatchFiles, ", ")))
	}
	if item.PatchHunks > 0 {
		parts = append(parts, fmt.Sprintf("hunks=%d", item.PatchHunks))
	}
	if item.IsLatestPatch {
		parts = append(parts, "latest")
	}
	if current := strings.TrimSpace(m.currentPatchPath()); current != "" && containsStringFold(item.PatchFiles, current) {
		parts = append(parts, "current target")
	}
	if item.ToolCalls > 0 {
		toolSummary := fmt.Sprintf("tools=%d", item.ToolCalls)
		if len(item.ToolNames) > 0 {
			toolSummary = fmt.Sprintf("%s [%s]", toolSummary, strings.Join(item.ToolNames, ", "))
		}
		parts = append(parts, toolSummary)
	}
	if item.ToolFailures > 0 {
		parts = append(parts, fmt.Sprintf("failures=%d", item.ToolFailures))
	}
	return strings.Join(parts, " | ")
}

func (m *Model) annotateAssistantPatch(index int) {
	if index < 0 || index >= len(m.chat.transcript) {
		return
	}
	if m.chat.transcript[index].Role != "assistant" {
		return
	}
	sections := parseUnifiedDiffSections(m.chat.transcript[index].Content)
	m.chat.transcript[index].PatchFiles = patchSectionPaths(sections)
	m.chat.transcript[index].PatchHunks = totalPatchHunks(sections)
}

func (m *Model) annotateAssistantToolUsage(index int) {
	if index < 0 || index >= len(m.chat.transcript) {
		return
	}
	if m.chat.transcript[index].Role != "assistant" || m.eng == nil || m.eng.Conversation == nil {
		return
	}
	msg, ok := m.matchAssistantConversationMessage(m.chat.transcript[index].Content)
	if !ok {
		return
	}
	m.chat.transcript[index].ToolCalls = len(msg.ToolCalls)
	m.chat.transcript[index].ToolFailures = 0
	if len(msg.ToolCalls) == 0 && len(msg.Results) == 0 {
		return
	}
	names := make([]string, 0, len(msg.ToolCalls))
	seen := map[string]struct{}{}
	for _, call := range msg.ToolCalls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for _, result := range msg.Results {
		if !result.Success {
			m.chat.transcript[index].ToolFailures++
		}
		if name := strings.TrimSpace(result.Name); name != "" {
			if _, ok := seen[name]; !ok {
				seen[name] = struct{}{}
				names = append(names, name)
			}
		}
	}
	m.chat.transcript[index].ToolNames = names
}

func (m Model) matchAssistantConversationMessage(content string) (types.Message, bool) {
	if m.eng == nil || m.eng.Conversation == nil {
		return types.Message{}, false
	}
	active := m.eng.Conversation.Active()
	if active == nil {
		return types.Message{}, false
	}
	want := strings.TrimSpace(content)
	messages := active.Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != types.RoleAssistant {
			continue
		}
		if strings.TrimSpace(msg.Content) == want {
			return msg, true
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != types.RoleAssistant {
			continue
		}
		if len(msg.ToolCalls) > 0 || len(msg.Results) > 0 {
			return msg, true
		}
	}
	return types.Message{}, false
}

func (m *Model) markLatestPatchInTranscript(patch string) {
	for i := range m.chat.transcript {
		m.chat.transcript[i].IsLatestPatch = false
	}
	patch = strings.TrimSpace(strings.ReplaceAll(patch, "\r\n", "\n"))
	if patch == "" {
		return
	}
	for i := len(m.chat.transcript) - 1; i >= 0; i-- {
		if m.chat.transcript[i].Role != "assistant" {
			continue
		}
		if strings.TrimSpace(extractUnifiedDiff(m.chat.transcript[i].Content)) == patch {
			m.chat.transcript[i].IsLatestPatch = true
			if len(m.chat.transcript[i].PatchFiles) == 0 {
				m.annotateAssistantPatch(i)
			}
			return
		}
	}
}
