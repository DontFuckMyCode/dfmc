package tui

import (
	"fmt"
	"strings"
	"time"
)

// renderStatusView is the legacy F2 panel renderer. Kept around as a
// reference for the panel rebuild — render_status.go has the active
// implementation (renderStatusViewV2). Deleted-after-stabilization; do
// NOT route through this. Tests still call it for the old text-shape
// regression so the new one stays comparable.
func (m Model) renderStatusView(width int) string {
	return m.renderStatusViewV2(width)
}

func (m Model) renderFilesView(width int) string {
	return m.renderFilesViewSized(width, 24)
}

// renderFilesViewSized delegates to the rebuilt 3-pane explorer in
// render_files.go. The legacy text-shape lives in git history; the V2
// renderer is the active implementation for the F3 panel.
func (m Model) renderFilesViewSized(width, height int) string {
	return m.renderFilesViewV2(width, height)
}

func (m Model) renderToolsView(width int) string {
	return m.renderToolsViewV2(width)
}

func (m Model) renderFooter(width int) string {
	maxWidth := max(width-4, 16)

	tab := m.tabs[m.activeTab]
	segments := []string{titleStyle.Render(" " + tab + " ")}
	segments = append(segments, m.footerSegments()...)
	if pinned := strings.TrimSpace(m.filesView.pinned); pinned != "" {
		segments = append(segments, accentStyle.Render("◆ "+truncateSingleLine(pinned, 22)))
	}
	if note := strings.TrimSpace(m.notice); note != "" {
		segments = append(segments, subtleStyle.Render("· ")+truncateSingleLine(note, 80))
	}
	sep := subtleStyle.Render("  ·  ")
	return truncateSingleLine(strings.Join(segments, sep), maxWidth)
}

func (m Model) footerSegments() []string {
	out := []string{}
	tokens, maxCtx := 0, 0
	if m.status.ContextIn != nil {
		tokens = m.status.ContextIn.TokenCount
		maxCtx = m.status.ContextIn.ProviderMaxContext
	}
	if maxCtx == 0 {
		maxCtx = m.status.ProviderProfile.MaxContext
	}
	if live := m.liveContextSnapshot(); live.ok {
		if live.windowTokens > 0 {
			tokens = live.windowTokens
		}
		if live.maxContext > 0 {
			maxCtx = live.maxContext
		}
	}
	out = append(out, renderContextBar(tokens, maxCtx, 10))
	if runtime := strings.TrimSpace(m.footerRuntimeSegment()); runtime != "" {
		out = append(out, runtime)
	}

	info := m.gitInfo
	if strings.TrimSpace(info.Branch) != "" {
		label := info.Branch
		if info.Detached {
			label = "(" + label + ")"
		}
		chip := accentStyle.Render("⎇ ") + boldStyle.Render(label)
		if info.Dirty {
			chip += warnStyle.Render("*")
		}
		out = append(out, chip)
	}
	if info.Inserted > 0 || info.Deleted > 0 {
		churn := okStyle.Render(fmt.Sprintf("+%d", info.Inserted)) +
			subtleStyle.Render(",") +
			failStyle.Render(fmt.Sprintf("-%d", info.Deleted))
		out = append(out, churn)
	}
	if !m.sessionStart.IsZero() {
		out = append(out, subtleStyle.Render("⏱ ")+boldStyle.Render(formatSessionDuration(time.Since(m.sessionStart))))
	}
	return out
}

func (m Model) footerRuntimeSegment() string {
	info := m.statsPanelInfo()
	parts := []string{}
	switch {
	case info.AgentActive:
		phase := strings.TrimSpace(info.AgentPhase)
		if phase == "" {
			phase = "working"
		}
		label := spinnerFrame(m.chat.spinnerFrame) + " " + humanizeAgentPhase(phase)
		if info.AgentMaxSteps > 0 {
			label += fmt.Sprintf(" %d/%d", max(info.AgentStep, 1), info.AgentMaxSteps)
		} else if info.AgentStep > 0 {
			label += fmt.Sprintf(" step %d", info.AgentStep)
		}
		parts = append(parts, accentStyle.Render(label))
	case info.Streaming:
		parts = append(parts, infoStyle.Render(spinnerFrame(m.chat.spinnerFrame)+" streaming"))
	case info.Parked:
		parts = append(parts, warnStyle.Render("parked"))
	}
	if info.ActiveTools > 0 {
		parts = append(parts, infoStyle.Render(fmt.Sprintf("tools %d", info.ActiveTools)))
	}
	if info.ActiveSubagents > 0 {
		parts = append(parts, accentStyle.Render(fmt.Sprintf("agents %d", info.ActiveSubagents)))
	}
	if tool := strings.TrimSpace(info.LastTool); tool != "" && (info.Streaming || info.AgentActive || info.LastStatus == "failed") {
		label := "last " + tool
		if info.LastStatus != "" {
			label += " " + info.LastStatus
		}
		if info.LastDurationMs > 0 {
			label += fmt.Sprintf(" %dms", info.LastDurationMs)
		}
		if info.LastStatus == "failed" {
			parts = append(parts, warnStyle.Render(label))
		} else {
			parts = append(parts, subtleStyle.Render(label))
		}
	}
	if info.QueuedCount > 0 {
		parts = append(parts, accentStyle.Render(fmt.Sprintf("queue %d", info.QueuedCount)))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, subtleStyle.Render(" / "))
}

func (m Model) workbenchRuntimeStatus() string {
	info := m.statsPanelInfo()
	parts := []string{}
	switch {
	case info.AgentActive:
		phase := strings.TrimSpace(info.AgentPhase)
		if phase == "" {
			phase = "working"
		}
		label := "working " + humanizeAgentPhase(phase)
		if info.AgentMaxSteps > 0 {
			label += fmt.Sprintf(" %d/%d", max(info.AgentStep, 1), info.AgentMaxSteps)
		} else if info.AgentStep > 0 {
			label += fmt.Sprintf(" step %d", info.AgentStep)
		}
		parts = append(parts, label)
	case info.Streaming:
		parts = append(parts, "streaming")
	case info.Parked:
		parts = append(parts, "parked")
	}
	if info.ActiveTools > 0 {
		parts = append(parts, fmt.Sprintf("tools %d", info.ActiveTools))
	}
	if info.ActiveSubagents > 0 {
		parts = append(parts, fmt.Sprintf("agents %d", info.ActiveSubagents))
	}
	if tool := strings.TrimSpace(info.LastTool); tool != "" && (info.Streaming || info.AgentActive) {
		parts = append(parts, "last "+tool)
	}
	if info.QueuedCount > 0 {
		parts = append(parts, fmt.Sprintf("queue %d", info.QueuedCount))
	}
	return strings.Join(parts, " / ")
}

func (m Model) renderHelpOverlay(width int) string {
	if width < 40 {
		width = 40
	}
	tab := m.tabs[m.activeTab]
	lines := []string{
		titleStyle.Render(" Keys ") + subtleStyle.Render("  ctrl+h to close"),
		"",
		boldStyle.Render(tab + " tab"),
	}
	for _, hint := range helpOverlayTabHints(tab) {
		lines = append(lines, "  "+hint)
	}
	lines = append(lines,
		"",
		boldStyle.Render("Global"),
		"  ctrl+p palette · f1/alt+1=chat f2/alt+2=files f3/alt+3=activity f4/alt+4=providers f5/alt+5=patch f6/alt+6=tools f7/alt+7=workflow f8/alt+8=memory f9/alt+9=codemap f10/alt+0=conversations alt+t=prompts f12=security · ctrl+i=status ctrl+y=plans ctrl+w=context ctrl+g=activity · ctrl+h/f11 help · ctrl+s stats",
		"  chat stats: alt+a overview · alt+s todos · alt+d tasks · alt+f agents · alt+p providers",
		"  ctrl+c/ctrl+q quit · ctrl+u clear chat input · esc cancels streaming turn (or dismisses parked banner)",
		"",
		boldStyle.Render("Chat composer"),
		"  ↑/↓ history · tab accept suggestion · @ mention file · / browse commands",
		"  @file:10-50 or @file#L10-L50 attaches a line range to the mention",
		"  ctrl+←/→ jump word · ctrl+w kill word · ctrl+k kill to end · ctrl+u clear line",
		"  ctrl+a/ctrl+e line home/end · home/end same · backspace deletes char",
		"  ctrl+t or /file open file picker (alias for @, useful on AltGr layouts)",
		"  /continue resumes a parked agent loop · /btw queues a note",
		"  /clear wipes transcript · /quit exits · /coach mutes notes · /hints toggles trajectory",
		"  /plan enters investigate-only mode · /code exits and re-enables mutations",
		"  /retry resends last user msg · /edit pulls last msg back to the composer",
	)
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		out = append(out, truncateSingleLine(ln, width))
	}
	return strings.Join(out, "\n")
}

func helpOverlayTabHints(tab string) []string {
	switch strings.TrimSpace(strings.ToLower(tab)) {
	case "chat":
		return []string{
			"ctrl+x send · enter newline · / commands · @ mention",
			"wheel · shift+↑/↓ · pgup/pgdn scroll transcript",
			"alt+a overview · alt+s todos · alt+d tasks · alt+f subagents · alt+p providers in the right stats panel",
			"when parked: ctrl+x resumes · esc dismisses · type a note first to steer",
		}
	case "status":
		return []string{
			"← → / h l move between cards · ↑ ↓ / j k row jump · home/g end/G",
			"enter jumps to detail tab · r refresh",
		}
	case "files":
		return []string{
			"↑↓ move · enter / → opens action menu · esc closes",
			"menu: ↑↓ pick · enter run · letters in [brackets] are accelerators",
		}
	case "patch":
		return []string{
			"↑↓ next/prev hunk · n/b next/prev file · enter / → action menu",
			"menu: apply · check · undo · focus · reload — accelerators: a/c/u/f/d",
		}
	case "workflow":
		return []string{
			"↑↓ move · enter select run / expand TODO · → action menu · esc back",
			"menu: stop · resume · copy ID into chat · open routing editor · refresh",
		}
	case "tools":
		return []string{
			"j/k select · enter run · e edit params · x reset · r rerun",
			"banner shows EDITING when param editor is open",
		}
	case "activity":
		return []string{
			"j/k scroll · pgup/pgdn page · g/G top/tail · p toggle follow · c clear",
		}
	case "memory":
		return []string{
			"↑↓ scroll · enter / → opens action menu (cycle tier · refresh · search · clear)",
			"single-letter accelerators (t/r//c) still work for power users",
		}
	case "codemap":
		return []string{
			"↑↓ scroll · enter / → opens action menu (cycle view · refresh · top/bottom)",
			"accelerators: v/r/g/G",
		}
	case "conversations":
		return []string{
			"↑↓ scroll · enter previews · → opens action menu (refresh · search · clear)",
			"accelerators: r/// c",
		}
	case "prompts":
		return []string{
			"↑↓ scroll · enter previews · → opens action menu (refresh · search · clear)",
			"accelerators: r/// c",
		}
	case "security":
		return []string{
			"↑↓ scroll · enter / → opens action menu (toggle view · rescan · search · clear)",
			"accelerators: v/r/// c",
		}
	case "plans":
		return []string{
			"e edit task · enter run · esc cancel edit · j/k scroll · c clear",
		}
	case "context":
		return []string{
			"e edit query · enter preview · esc cancel edit · c clear",
		}
	case "providers":
		return []string{
			"j/k scroll · r refresh · g/G top/bottom",
		}
	case "orchestrate":
		return []string{
			"alt+r jumps here · live hierarchy of agents/subagents/todos/drive/tokens",
			"read-only panel — drive control from chat (/drive) · todo control via agent",
		}
	case "shortcuts":
		return []string{
			"alt+h jumps here from any tab · /shortcuts and /keys also open this",
			"per-tab quick hints: ctrl+h overlay · /help in chat for the full catalog",
		}
	default:
		return []string{"f1=chat f2=files f3=activity f4=providers f5=patch f6=tools f7=workflow f8=memory f9=codemap f10=conversations alt+t=prompts f12=security · ctrl+p palette · ctrl+h/f11 help · ctrl+q quit"}
	}
}
