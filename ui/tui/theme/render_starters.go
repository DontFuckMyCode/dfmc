package theme

// render_starters.go — first-run starter prompt grid plus the small
// streaming-indicator and parking-banner widgets the chat surface
// shows around in-flight work. Companion siblings:
//
//   - render.go               role helpers, section header, todo strip,
//                             runtime card, TruncateSingleLine
//   - render_chat_header.go   CHAT header bar + workflow focus card
//   - render_message.go       message header, bubble, wrap helpers,
//                             divider, input box

import (
	"fmt"
	"strings"
)

type StarterPrompt struct {
	Key   string
	Title string
	Cmd   string
	Hint  string
}

func DefaultStarterPrompts() []StarterPrompt {
	return []StarterPrompt{
		{Key: "1", Title: "Review this project", Cmd: "/review", Hint: "quality, risks, suggestions"},
		{Key: "2", Title: "Explain a file", Cmd: "/explain @", Hint: "press @ to pick a file"},
		{Key: "3", Title: "Analyze architecture", Cmd: "/analyze", Hint: "symbols, hotspots, deps"},
		{Key: "4", Title: "Map the codebase", Cmd: "/map", Hint: "dependency graph, cycles"},
		{Key: "5", Title: "Find bugs & smells", Cmd: "/scan", Hint: "security + correctness scan"},
		{Key: "6", Title: "Draft a refactor plan", Cmd: "/refactor", Hint: "stepwise, low-risk"},
	}
}

func StarterTemplateForDigit(r rune) (string, bool) {
	if r < '1' || r > '9' {
		return "", false
	}
	idx := int(r - '1')
	prompts := DefaultStarterPrompts()
	if idx >= len(prompts) {
		return "", false
	}
	return prompts[idx].Cmd, true
}

func RenderStarterPrompts(width int, configured bool) []string {
	prompts := DefaultStarterPrompts()
	if width <= 0 {
		width = 100
	}
	lines := []string{""}
	if !configured {
		lines = append(lines,
			FailStyle.Bold(true).Render("⚠ No provider configured"),
			SubtleStyle.Render("  Press ")+AccentStyle.Bold(true).Render("f5")+SubtleStyle.Render(" for the Workflow tab, or type ")+CodeStyle.Render("/provider")+SubtleStyle.Render(" to pick one — starters need a model to run."),
			"",
		)
	}
	lines = append(lines,
		BoldStyle.Render(AccentStyle.Render("Welcome — what would you like DFMC to do?")),
		SubtleStyle.Render("  Pick a starter, type a question, or use "+CodeStyle.Render("@file")+" / "+CodeStyle.Render("/command")+"."),
		"",
	)
	for _, p := range prompts {
		key := TitleStyle.Render(" " + p.Key + " ")
		title := BoldStyle.Render(p.Title)
		cmd := CodeStyle.Render(p.Cmd)
		hint := SubtleStyle.Render("— " + p.Hint)
		raw := fmt.Sprintf("   %-2s  %-26s  %-18s  %s", p.Key, p.Title, p.Cmd, "— "+p.Hint)
		if len([]rune(raw)) > width {
			lines = append(lines, TruncateSingleLine(raw, width))
			continue
		}
		lines = append(lines, "  "+key+"  "+title+"  "+cmd+"  "+hint)
	}
	lines = append(lines,
		"",
		SubtleStyle.Render("  Tips: "+AccentStyle.Render("enter")+" send · "+AccentStyle.Render("alt+enter")+" newline · "+AccentStyle.Render("@")+" file mention · "+AccentStyle.Render("/")+" commands · "+AccentStyle.Render("ctrl+p")+" palette · "+AccentStyle.Render("f1-f12")+" tabs"),
		SubtleStyle.Render("  Runtime: "+CodeStyle.Render("/model")+" changes model · "+AccentStyle.Render("alt+p")+" opens the compact status panel"),
	)
	return lines
}

func RenderStreamingIndicator(phase string, frame int) string {
	phase = strings.TrimSpace(phase)
	if phase == "" {
		phase = "drafting reply"
	}
	glyph := SpinnerFrame(frame)
	// `esc cancels` was a lie — esc dismisses overlay states
	// (resume prompt, mention picker, next-actions strip) but does
	// NOT cancel the active stream. The cancel key is ctrl+c (and
	// ctrl+q) per handleChatControlShortcut. The streaming hint
	// must name the real key so users in a runaway response don't
	// keep mashing esc and watching nothing happen.
	return InfoStyle.Bold(true).Render(glyph+" "+phase) + " " + SubtleStyle.Render("· ctrl+c cancels · tokens stream live")
}

func RenderResumeBanner(step, maxSteps, width int) string {
	if width < 20 {
		width = 20
	}
	title := WarnStyle.Bold(true).Render("⏸ Tool loop parked")
	progress := ""
	if maxSteps > 0 {
		progress = SubtleStyle.Render(fmt.Sprintf(" at step %d/%d", step, maxSteps))
	} else if step > 0 {
		progress = SubtleStyle.Render(fmt.Sprintf(" at step %d", step))
	}
	hint := SubtleStyle.Render("  enter resumes") + SubtleStyle.Render(" · ") +
		SubtleStyle.Render("esc dismisses") + SubtleStyle.Render(" · ") +
		SubtleStyle.Render("type a note first to steer /continue")
	head := TruncateSingleLine(title+progress, width)
	body := TruncateSingleLine(hint, width)
	return ResumeBannerStyle.Width(width).Render(head + "\n" + body)
}
