package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestViewIncludesWorkbenchPanels(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.width = 100

	view := m.View()
	for _, needle := range []string{"DFMC WORKBENCH", "Chat", "Status", "Files", "Patch", "Setup", "Tools"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("expected view to contain %q, got:\n%s", needle, view)
		}
	}
}

func TestTabSwitching(t *testing.T) {
	m := NewModel(context.Background(), nil)

	chatModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	chatNext, ok := chatModel.(Model)
	if !ok {
		t.Fatalf("expected Model after tab key on chat tab, got %T", chatModel)
	}
	if chatNext.activeTab != 0 {
		t.Fatalf("expected tab key to stay on chat tab for autocomplete, got %d", chatNext.activeTab)
	}

	chatNext.activeTab = 1
	nextModel, _ := chatNext.Update(tea.KeyMsg{Type: tea.KeyTab})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after tab key, got %T", nextModel)
	}
	if next.activeTab != 2 {
		t.Fatalf("expected active tab 2 after tab, got %d", next.activeTab)
	}

	prevModel, _ := next.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	prev, ok := prevModel.(Model)
	if !ok {
		t.Fatalf("expected Model after shift+tab, got %T", prevModel)
	}
	if prev.activeTab != 1 {
		t.Fatalf("expected active tab 1 after shift+tab, got %d", prev.activeTab)
	}
}

func TestAltNumberSwitchesTabs(t *testing.T) {
	m := NewModel(context.Background(), nil)

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2"), Alt: true})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after alt+2, got %T", nextModel)
	}
	if next.activeTab != 1 {
		t.Fatalf("expected active tab 1 after alt+2, got %d", next.activeTab)
	}

	finalModel, _ := next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("6"), Alt: true})
	final, ok := finalModel.(Model)
	if !ok {
		t.Fatalf("expected Model after alt+6, got %T", finalModel)
	}
	if final.activeTab != 5 {
		t.Fatalf("expected active tab 5 after alt+6, got %d", final.activeTab)
	}
}

func TestCtrlPOpensChatCommandPalette(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 3
	m.input = "old"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after ctrl+p, got %T", nextModel)
	}
	if next.activeTab != 0 {
		t.Fatalf("expected chat tab after ctrl+p, got %d", next.activeTab)
	}
	if next.input != "/" {
		t.Fatalf("expected slash command palette seed, got %q", next.input)
	}
}

func TestChatAllowsTypingQAndCtrlCStillQuits(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	typedModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	typed, ok := typedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after q key, got %T", typedModel)
	}
	if typed.input != "q" {
		t.Fatalf("expected q to be inserted into chat input, got %q", typed.input)
	}
	if cmd != nil {
		t.Fatalf("expected q key not to trigger quit command")
	}

	_, quitCmd := typed.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if quitCmd == nil {
		t.Fatal("expected ctrl+c to return quit command")
	}
	if _, ok := quitCmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected ctrl+c to emit tea.QuitMsg")
	}
}

func TestChatSlashProviderAndModelCommands(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = "/provider openai"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after provider command, got %T", nextModel)
	}
	if next.currentProvider() != "openai" {
		t.Fatalf("expected provider override openai, got %q", next.currentProvider())
	}
	if len(next.transcript) == 0 || next.transcript[len(next.transcript)-1].Role != "system" {
		t.Fatalf("expected system transcript entry after provider command, got %#v", next.transcript)
	}

	next.input = "/model gpt-5.4"
	updatedModel, _ := next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated, ok := updatedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after model command, got %T", updatedModel)
	}
	if updated.currentModel() != "gpt-5.4" {
		t.Fatalf("expected model override gpt-5.4, got %q", updated.currentModel())
	}
}

func TestChatSlashProviderPersistWritesProjectConfig(t *testing.T) {
	eng := newTUITestEngine(t)
	root := t.TempDir()
	eng.ProjectRoot = root

	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = "/provider openai gpt-5.4 --persist"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after provider persist command, got %T", nextModel)
	}

	path := filepath.Join(root, ".dfmc", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	doc := map[string]any{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	providers, ok := doc["providers"].(map[string]any)
	if !ok {
		t.Fatalf("expected providers map, got %#v", doc["providers"])
	}
	if got := providers["primary"]; got != "openai" {
		t.Fatalf("expected providers.primary=openai, got %#v", got)
	}
	profiles, ok := providers["profiles"].(map[string]any)
	if !ok {
		t.Fatalf("expected providers.profiles map, got %#v", providers["profiles"])
	}
	openaiProfile, ok := profiles["openai"].(map[string]any)
	if !ok {
		t.Fatalf("expected openai profile map, got %#v", profiles["openai"])
	}
	if got := openaiProfile["model"]; got != "gpt-5.4" {
		t.Fatalf("expected openai model persisted, got %#v", got)
	}
	if !strings.Contains(next.notice, "saved to") {
		t.Fatalf("expected persist notice, got %q", next.notice)
	}
}

func TestChatSlashMenuTabCompletesAndRunsCommand(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = "/prov"

	completedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	completed, ok := completedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after tab completion, got %T", completedModel)
	}
	if completed.input != "/providers" {
		t.Fatalf("expected slash completion to /providers, got %q", completed.input)
	}

	finalModel, _ := completed.Update(tea.KeyMsg{Type: tea.KeyEnter})
	final, ok := finalModel.(Model)
	if !ok {
		t.Fatalf("expected Model after enter on slash command, got %T", finalModel)
	}
	if len(final.transcript) == 0 || final.transcript[len(final.transcript)-1].Role != "system" {
		t.Fatalf("expected system transcript entry after /providers, got %#v", final.transcript)
	}
	if !strings.Contains(final.transcript[len(final.transcript)-1].Content, "Providers:") {
		t.Fatalf("expected providers output in transcript, got %#v", final.transcript[len(final.transcript)-1])
	}
}

func TestChatSlashProviderArgTabCompletesProviderName(t *testing.T) {
	eng := newTUITestEngine(t)
	eng.Config.Providers.Profiles = map[string]config.ModelConfig{
		"anthropic": {Model: "claude-sonnet-4-6"},
		"openai":    {Model: "gpt-5.4"},
	}

	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = "/provider op"

	completedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	completed, ok := completedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after provider arg tab completion, got %T", completedModel)
	}
	if completed.input != "/provider openai" {
		t.Fatalf("expected /provider openai completion, got %q", completed.input)
	}
}

func TestChatSlashProviderArgDownThenTabSelectsSecondSuggestion(t *testing.T) {
	eng := newTUITestEngine(t)
	eng.Config.Providers.Profiles = map[string]config.ModelConfig{
		"anthropic": {Model: "claude-sonnet-4-6"},
		"openai":    {Model: "gpt-5.4"},
	}

	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = "/provider "

	downModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	down, ok := downModel.(Model)
	if !ok {
		t.Fatalf("expected Model after provider arg down, got %T", downModel)
	}
	if down.slashArgIndex != 1 {
		t.Fatalf("expected arg index 1 after down, got %d", down.slashArgIndex)
	}

	completedModel, _ := down.Update(tea.KeyMsg{Type: tea.KeyTab})
	completed, ok := completedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after provider arg tab, got %T", completedModel)
	}
	if completed.input != "/provider openai" {
		t.Fatalf("expected second provider completion to /provider openai, got %q", completed.input)
	}
}

func TestChatSlashToolArgTabCompletesToolName(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = "/tool re"

	completedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	completed, ok := completedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after tool arg tab completion, got %T", completedModel)
	}
	if completed.input != "/tool read_file" {
		t.Fatalf("expected /tool read_file completion, got %q", completed.input)
	}
}

func TestRenderChatViewShowsQuickActionsForNaturalLanguage(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "note.txt"), "alpha\nbeta\n")
	eng := newTUITestEngine(t)
	eng.ProjectRoot = root

	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.files = []string{"note.txt"}
	m.input = "read note.txt"

	view := m.renderChatView(120)
	if !strings.Contains(view, "Quick Actions") || !strings.Contains(view, "/read note.txt 1 200") {
		t.Fatalf("expected quick actions block in chat view, got:\n%s", view)
	}
	if !strings.Contains(view, "/grep note") {
		t.Fatalf("expected secondary grep quick action in chat view, got:\n%s", view)
	}
}

func TestChatTabPreparesQuickActionFromNaturalLanguage(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "note.txt"), "alpha\nbeta\n")
	eng := newTUITestEngine(t)
	eng.ProjectRoot = root

	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.files = []string{"note.txt"}
	m.input = "read note.txt"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after quick-action tab, got %T", nextModel)
	}
	if next.input != "/read note.txt 1 200" {
		t.Fatalf("expected quick action to prepare slash command, got %q", next.input)
	}
}

func TestChatDownThenTabPreparesSecondQuickAction(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "note.txt"), "alpha\nbeta\n")
	eng := newTUITestEngine(t)
	eng.ProjectRoot = root

	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.files = []string{"note.txt"}
	m.input = "read note.txt"

	downModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	down, ok := downModel.(Model)
	if !ok {
		t.Fatalf("expected Model after quick-action down, got %T", downModel)
	}
	if down.quickActionIndex != 1 {
		t.Fatalf("expected quick action index 1 after down, got %d", down.quickActionIndex)
	}

	nextModel, _ := down.Update(tea.KeyMsg{Type: tea.KeyTab})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after quick-action tab, got %T", nextModel)
	}
	if !strings.HasPrefix(next.input, "/grep ") {
		t.Fatalf("expected second quick action to prepare grep command, got %q", next.input)
	}
}

func TestChatEnterRunsSelectedQuickAction(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "note.txt"), "alpha\nbeta\n")
	eng := newTUITestEngine(t)
	eng.ProjectRoot = root

	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.files = []string{"note.txt"}
	m.input = "read note.txt"

	downModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	down, ok := downModel.(Model)
	if !ok {
		t.Fatalf("expected Model after quick-action down, got %T", downModel)
	}

	nextModel, cmd := down.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after quick-action enter, got %T", nextModel)
	}
	if cmd == nil {
		t.Fatal("expected tool command from selected quick action")
	}
	if !next.chatToolPending || next.chatToolName != "grep_codebase" {
		t.Fatalf("expected selected quick action grep_codebase to run, got pending=%v name=%q", next.chatToolPending, next.chatToolName)
	}
}

func TestChatSlashReadRunsToolAndAppendsResult(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "note.txt"), "line-1\nline-2\n")
	eng := newTUITestEngine(t)
	eng.ProjectRoot = root

	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = "/read note.txt 1 1"

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after /read command, got %T", nextModel)
	}
	if cmd == nil {
		t.Fatal("expected tool command from /read")
	}
	if !next.chatToolPending || next.chatToolName != "read_file" {
		t.Fatalf("expected pending chat tool read_file, got pending=%v name=%q", next.chatToolPending, next.chatToolName)
	}

	finalModel, _ := next.Update(cmd())
	final, ok := finalModel.(Model)
	if !ok {
		t.Fatalf("expected Model after read_file tool result, got %T", finalModel)
	}
	if final.chatToolPending {
		t.Fatal("expected chat tool pending to clear after result")
	}
	if len(final.transcript) == 0 {
		t.Fatal("expected transcript entries after /read flow")
	}
	last := final.transcript[len(final.transcript)-1]
	if last.Role != "system" || !strings.Contains(last.Content, "Tool result: read_file success") || !strings.Contains(last.Content, "line-1") {
		t.Fatalf("expected read tool result in system transcript, got %#v", last)
	}
}

func TestChatSlashRunCommandStreamsToolResultToTranscript(t *testing.T) {
	eng := newTUITestEngine(t)
	eng.ProjectRoot = t.TempDir()

	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = "/run go version"

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after /run command, got %T", nextModel)
	}
	if cmd == nil {
		t.Fatal("expected tool command from /run")
	}
	finalModel, _ := next.Update(cmd())
	final, ok := finalModel.(Model)
	if !ok {
		t.Fatalf("expected Model after run_command result, got %T", finalModel)
	}
	if len(final.transcript) == 0 {
		t.Fatal("expected transcript entries after /run flow")
	}
	last := final.transcript[len(final.transcript)-1]
	if last.Role != "system" || !strings.Contains(last.Content, "Tool result: run_command") {
		t.Fatalf("expected run_command result in transcript, got %#v", last)
	}
}

func TestParseListDirChatArgs(t *testing.T) {
	params, err := parseListDirChatArgs([]string{"src", "--recursive", "--max", "42"})
	if err != nil {
		t.Fatalf("parseListDirChatArgs: %v", err)
	}
	if got := params["path"]; got != "src" {
		t.Fatalf("expected path src, got %#v", got)
	}
	if got := params["recursive"]; got != true {
		t.Fatalf("expected recursive=true, got %#v", got)
	}
	if got := params["max_entries"]; got != 42 {
		t.Fatalf("expected max_entries=42, got %#v", got)
	}
}

func TestChatSlashReadSupportsQuotedPath(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "note file.txt"), "line-a\nline-b\n")
	eng := newTUITestEngine(t)
	eng.ProjectRoot = root

	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = `/read "note file.txt" 1 1`

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after quoted /read command, got %T", nextModel)
	}
	if cmd == nil {
		t.Fatal("expected tool command from quoted /read")
	}
	finalModel, _ := next.Update(cmd())
	final, ok := finalModel.(Model)
	if !ok {
		t.Fatalf("expected Model after quoted read_file result, got %T", finalModel)
	}
	if len(final.transcript) == 0 {
		t.Fatal("expected transcript entries after quoted /read")
	}
	last := final.transcript[len(final.transcript)-1]
	if last.Role != "system" || !strings.Contains(last.Content, "Tool result: read_file success") || !strings.Contains(last.Content, "line-a") {
		t.Fatalf("expected quoted read_file result in transcript, got %#v", last)
	}
}

func TestChatSlashReadArgTabCompletionQuotesPath(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.files = []string{"note file.txt", "README.md"}
	m.input = "/read note"

	completedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	completed, ok := completedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after read arg tab completion, got %T", completedModel)
	}
	if completed.input != `/read "note file.txt"` {
		t.Fatalf("expected quoted read path completion, got %q", completed.input)
	}
}

func TestChatSlashToolSupportsQuotedParams(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "note file.txt"), "tool-line\n")
	eng := newTUITestEngine(t)
	eng.ProjectRoot = root

	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = `/tool read_file path="note file.txt" line_start=1 line_end=1`

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after quoted /tool command, got %T", nextModel)
	}
	if cmd == nil {
		t.Fatal("expected tool command from quoted /tool")
	}
	finalModel, _ := next.Update(cmd())
	final, ok := finalModel.(Model)
	if !ok {
		t.Fatalf("expected Model after /tool result, got %T", finalModel)
	}
	if len(final.transcript) == 0 {
		t.Fatal("expected transcript entries after /tool")
	}
	last := final.transcript[len(final.transcript)-1]
	if last.Role != "system" || !strings.Contains(last.Content, "Tool result: read_file success") || !strings.Contains(last.Content, "tool-line") {
		t.Fatalf("expected read_file tool result in transcript, got %#v", last)
	}
}

func TestChatSlashToolParamKeyTabCompletion(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = "/tool read_file p"

	completedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	completed, ok := completedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after tool param key tab completion, got %T", completedModel)
	}
	if completed.input != "/tool read_file path=" {
		t.Fatalf("expected /tool read_file path= completion, got %q", completed.input)
	}
}

func TestChatSlashToolParamValueTabCompletionQuotesPath(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.files = []string{"note file.txt", "README.md"}
	m.input = "/tool read_file path=no"

	completedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	completed, ok := completedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after tool param value tab completion, got %T", completedModel)
	}
	if completed.input != `/tool read_file path="note file.txt"` {
		t.Fatalf("expected quoted /tool path completion, got %q", completed.input)
	}
}

func TestChatSlashCommandParseErrorRemainsLocal(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.input = `/read "broken`

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after parse-error command, got %T", nextModel)
	}
	if cmd != nil {
		t.Fatalf("expected no tool/stream command on parse error, got %#v", cmd)
	}
	if next.sending {
		t.Fatal("expected parse-error slash input to stay local and not stream")
	}
	if len(next.transcript) == 0 || next.transcript[len(next.transcript)-1].Role != "system" || !strings.Contains(next.transcript[len(next.transcript)-1].Content, "Command parse error:") {
		t.Fatalf("expected local parse error transcript message, got %#v", next.transcript)
	}
}

func TestChatNaturalReadIntentTriggersTool(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "note.txt"), "hello\nworld\n")
	eng := newTUITestEngine(t)
	eng.ProjectRoot = root

	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = "read note.txt"

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after natural read intent, got %T", nextModel)
	}
	if cmd == nil {
		t.Fatal("expected tool cmd from natural read intent")
	}
	if next.sending {
		t.Fatal("expected natural read intent to run tool first instead of stream send")
	}
	if !next.chatToolPending || next.chatToolName != "read_file" {
		t.Fatalf("expected pending read_file tool, got pending=%v name=%q", next.chatToolPending, next.chatToolName)
	}
	if len(next.transcript) < 2 || next.transcript[0].Role != "user" || !strings.Contains(next.transcript[0].Content, "read note.txt") {
		t.Fatalf("expected user transcript entry before auto tool run, got %#v", next.transcript)
	}
	if !strings.Contains(next.transcript[1].Content, "Auto action: detected file read intent") {
		t.Fatalf("expected auto action transcript note, got %#v", next.transcript[1])
	}

	finalModel, _ := next.Update(cmd())
	final, ok := finalModel.(Model)
	if !ok {
		t.Fatalf("expected Model after natural read tool result, got %T", finalModel)
	}
	last := final.transcript[len(final.transcript)-1]
	if last.Role != "system" || !strings.Contains(last.Content, "Tool result: read_file") || !strings.Contains(last.Content, "hello") {
		t.Fatalf("expected read_file result message, got %#v", last)
	}
}

func TestChatNaturalPromptWithoutIntentStillStreams(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.input = "please explain this architecture"

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after normal prompt, got %T", nextModel)
	}
	if cmd == nil {
		t.Fatal("expected stream wait command for normal prompt")
	}
	if !next.sending {
		t.Fatal("expected normal prompt to enter streaming state")
	}
	if next.chatToolPending {
		t.Fatal("did not expect auto tool pending for normal prompt")
	}
}

func TestAutoToolIntentFromQuestionTurkishList(t *testing.T) {
	m := NewModel(context.Background(), nil)
	name, params, reason, ok := m.autoToolIntentFromQuestion("listele src recursive")
	if !ok {
		t.Fatal("expected list intent detection")
	}
	if name != "list_dir" {
		t.Fatalf("expected list_dir intent, got %q", name)
	}
	if params["path"] != "src" {
		t.Fatalf("expected path src, got %#v", params["path"])
	}
	if params["recursive"] != true {
		t.Fatalf("expected recursive=true, got %#v", params["recursive"])
	}
	if !strings.Contains(reason, "listing") {
		t.Fatalf("expected listing reason, got %q", reason)
	}
}

func TestHandleEngineEventToolCallUpdatesActivityWithoutTranscriptNoise(t *testing.T) {
	// Signal-density rule: tool:call events feed chips + activity log +
	// runtime card, but must not flood the transcript with narration — the
	// transcript is reserved for real state changes (errors, parks, compactions).
	m := NewModel(context.Background(), nil)
	m.sending = true
	m.transcript = []chatLine{
		{Role: "user", Content: "scan"},
		{Role: "assistant", Content: ""},
	}
	m.streamIndex = 1

	next := m.handleEngineEvent(engine.Event{
		Type: "tool:call",
		Payload: map[string]any{
			"tool":           "read_file",
			"step":           2,
			"params_preview": "path=internal/engine/engine.go line_start=1 line_end=80",
		},
	})
	if len(next.activityLog) == 0 || !strings.Contains(next.activityLog[len(next.activityLog)-1], "read_file") {
		t.Fatalf("expected activity log update, got %#v", next.activityLog)
	}
	if len(next.transcript) != 2 {
		t.Fatalf("expected transcript untouched by tool:call, got %#v", next.transcript)
	}
}

func TestHandleEngineEventToolResultUpdatesActivityWithoutTranscriptWhenIdle(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.sending = false

	next := m.handleEngineEvent(engine.Event{
		Type: "tool:result",
		Payload: map[string]any{
			"tool":       "grep_codebase",
			"success":    true,
			"durationMs": 77,
		},
	})
	if len(next.activityLog) == 0 || !strings.Contains(next.activityLog[0], "grep_codebase") {
		t.Fatalf("expected activity line for tool result, got %#v", next.activityLog)
	}
	if len(next.transcript) != 0 {
		t.Fatalf("expected no transcript update while idle, got %#v", next.transcript)
	}
}

func TestRenderChatViewSurfacesToolEventsViaRuntimeCard(t *testing.T) {
	// Signal-density rule: tool progress lives in the runtime card and chips,
	// not in the transcript. Legacy side panels (Live Activity / Tool Timeline)
	// are gone, and the transcript no longer echoes every call.
	m := NewModel(context.Background(), nil)
	m.sending = true
	m.transcript = []chatLine{
		{Role: "user", Content: "scan"},
		{Role: "assistant", Content: ""},
	}
	m.streamIndex = 1
	m = m.handleEngineEvent(engine.Event{
		Type: "tool:call",
		Payload: map[string]any{
			"tool":           "read_file",
			"step":           1,
			"params_preview": "path=note.txt",
		},
	})

	view := m.renderChatView(120)
	if !strings.Contains(view, "read_file") {
		t.Fatalf("expected read_file chip/card visible in chat view, got:\n%s", view)
	}
	if strings.Contains(view, "Agent tool call: read_file") {
		t.Fatalf("tool:call should not narrate into the transcript, got:\n%s", view)
	}
	if strings.Contains(view, "Live Activity") || strings.Contains(view, "Tool Timeline") {
		t.Fatalf("legacy side panels should be removed, got:\n%s", view)
	}
}

func TestRenderChatViewShowsSlashAssistForProviderCommand(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = "/provider "

	view := m.renderChatView(120)
	if !strings.Contains(view, "Slash Assist") || !strings.Contains(view, "Usage: /provider NAME [MODEL] [--persist]") {
		t.Fatalf("expected provider slash assist hints in chat view, got:\n%s", view)
	}
}

func TestRenderChatViewShowsCommandArgSuggestions(t *testing.T) {
	eng := newTUITestEngine(t)
	eng.Config.Providers.Profiles = map[string]config.ModelConfig{
		"anthropic": {Model: "claude-sonnet-4-6"},
		"openai":    {Model: "gpt-5.4"},
	}

	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = "/provider op"

	view := m.renderChatView(120)
	if !strings.Contains(view, "Command Args") || !strings.Contains(view, "openai") {
		t.Fatalf("expected command arg suggestion section in chat view, got:\n%s", view)
	}
}

func TestRenderChatViewShowsToolCommandArgSuggestions(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = "/tool read_file p"

	view := m.renderChatView(120)
	if !strings.Contains(view, "Command Args") || !strings.Contains(view, "path=") {
		t.Fatalf("expected tool command arg suggestion section in chat view, got:\n%s", view)
	}
}

func TestProviderPickerItemsIncludeMetadata(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.status = eng.Status()

	items := m.providerPickerItems()
	if len(items) == 0 {
		t.Fatal("expected provider picker items")
	}
	found := false
	for _, item := range items {
		if item.Value == "openai" {
			found = true
			if !strings.Contains(item.Description, "openai") && item.Description == "" {
				t.Fatalf("expected provider description metadata, got %#v", item)
			}
			if item.Meta == "" {
				t.Fatalf("expected provider meta metadata, got %#v", item)
			}
		}
	}
	if !found {
		t.Fatalf("expected openai in provider picker items, got %#v", items)
	}
}

func TestRenderChatViewShowsCommandPickerItemDetails(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m = m.startCommandPicker("provider", "", false)

	view := m.renderChatView(120)
	if !strings.Contains(view, "Provider Picker") {
		t.Fatalf("expected provider picker heading, got:\n%s", view)
	}
	if !strings.Contains(view, "configured") && !strings.Contains(view, "unconfigured") {
		t.Fatalf("expected picker item metadata in chat view, got:\n%s", view)
	}
}

func TestChatSlashToolWithoutArgsOpensToolPicker(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = "/tool"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after /tool, got %T", nextModel)
	}
	if !next.commandPickerActive || next.commandPickerKind != "tool" {
		t.Fatalf("expected tool picker to open, got active=%v kind=%q", next.commandPickerActive, next.commandPickerKind)
	}
}

func TestChatSlashReadWithoutArgsOpensReadPicker(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.files = []string{"README.md", "internal/engine/engine.go"}
	m.input = "/read"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after /read, got %T", nextModel)
	}
	if !next.commandPickerActive || next.commandPickerKind != "read" {
		t.Fatalf("expected read picker to open, got active=%v kind=%q", next.commandPickerActive, next.commandPickerKind)
	}
}

func TestToolPickerEnterPreparesToolCommand(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m = m.startCommandPicker("tool", "read_file", false)

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after tool picker enter, got %T", nextModel)
	}
	if next.commandPickerActive {
		t.Fatal("expected tool picker to close after enter")
	}
	if next.input != "/tool read_file " {
		t.Fatalf("expected prepared tool command, got %q", next.input)
	}
}

func TestReadPickerEnterPreparesReadCommand(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.files = []string{"README.md", "docs/My File.md"}
	m = m.startCommandPicker("read", "docs/My File.md", false)

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after read picker enter, got %T", nextModel)
	}
	if next.commandPickerActive {
		t.Fatal("expected read picker to close after enter")
	}
	if next.input != "/read \"docs/My File.md\" " {
		t.Fatalf("expected prepared quoted read command, got %q", next.input)
	}
}

func TestChatSlashRunWithoutArgsOpensRunPicker(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.files = []string{"go.mod"}
	m.input = "/run"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after /run, got %T", nextModel)
	}
	if !next.commandPickerActive || next.commandPickerKind != "run" {
		t.Fatalf("expected run picker to open, got active=%v kind=%q", next.commandPickerActive, next.commandPickerKind)
	}
}

func TestChatSlashGrepWithoutArgsOpensGrepPicker(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.files = []string{"internal/engine/engine.go"}
	m.input = "/grep"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after /grep, got %T", nextModel)
	}
	if !next.commandPickerActive || next.commandPickerKind != "grep" {
		t.Fatalf("expected grep picker to open, got active=%v kind=%q", next.commandPickerActive, next.commandPickerKind)
	}
}

func TestRunPickerEnterPreparesRunCommand(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m = m.startCommandPicker("run", "go test ./...", false)

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after run picker enter, got %T", nextModel)
	}
	if !strings.HasPrefix(next.input, "/run go test ./...") {
		t.Fatalf("expected prepared run command, got %q", next.input)
	}
}

func TestGrepPickerEnterPreparesGrepCommand(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m = m.startCommandPicker("grep", "TODO", false)

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after grep picker enter, got %T", nextModel)
	}
	if next.input != "/grep TODO" {
		t.Fatalf("expected prepared grep command, got %q", next.input)
	}
}

func TestHandleEngineEventContextBuiltUpdatesActivity(t *testing.T) {
	m := NewModel(context.Background(), nil)

	next := m.handleEngineEvent(engine.Event{
		Type: "context:built",
		Payload: map[string]any{
			"files":       4,
			"tokens":      980,
			"task":        "review",
			"compression": "aggressive",
		},
	})
	if len(next.activityLog) == 0 {
		t.Fatalf("expected context event activity log update, got %#v", next.activityLog)
	}
	if !strings.Contains(next.activityLog[len(next.activityLog)-1], "Context built: 4 files, 980 tokens") {
		t.Fatalf("unexpected context activity line: %#v", next.activityLog[len(next.activityLog)-1])
	}
}

func TestHandleEngineEventAgentLoopLifecycle(t *testing.T) {
	m := NewModel(context.Background(), nil)

	started := m.handleEngineEvent(engine.Event{
		Type: "agent:loop:start",
		Payload: map[string]any{
			"provider":       "openai",
			"model":          "gpt-5.4",
			"max_tool_steps": 6,
			"context_files":  4,
			"context_tokens": 900,
		},
	})
	if !started.agentLoopActive || started.agentLoopMaxToolStep != 6 || started.agentLoopPhase != "starting" {
		t.Fatalf("expected active runtime after loop start, got %#v", started)
	}

	thinking := started.handleEngineEvent(engine.Event{
		Type: "agent:loop:thinking",
		Payload: map[string]any{
			"step":           2,
			"max_tool_steps": 6,
			"tool_rounds":    1,
		},
	})
	if !thinking.agentLoopActive || thinking.agentLoopStep != 2 || thinking.agentLoopToolRounds != 1 || thinking.agentLoopPhase != "thinking" {
		t.Fatalf("expected thinking state update, got %#v", thinking)
	}

	completed := thinking.handleEngineEvent(engine.Event{
		Type: "provider:complete",
		Payload: map[string]any{
			"provider": "openai",
			"model":    "gpt-5.4",
			"tokens":   1234,
		},
	})
	if completed.agentLoopActive {
		t.Fatalf("expected runtime to finish on provider complete, got %#v", completed)
	}
	if len(completed.activityLog) == 0 || !strings.Contains(completed.activityLog[len(completed.activityLog)-1], "Provider complete") {
		t.Fatalf("expected provider complete activity line, got %#v", completed.activityLog)
	}
}

func TestRenderChatViewShowsAgentRuntimeCard(t *testing.T) {
	// Signal-density rule: the header owns phase/step/provider/model so the
	// runtime card only surfaces what the header doesn't — tool rounds and
	// the last tool's status/duration.
	m := NewModel(context.Background(), nil)
	m.status = engine.Status{Provider: "openai", Model: "gpt-5.4"}
	m.agentLoopActive = true
	m.agentLoopPhase = "tool-result"
	m.agentLoopStep = 2
	m.agentLoopMaxToolStep = 6
	m.agentLoopToolRounds = 2
	m.agentLoopProvider = "openai"
	m.agentLoopModel = "gpt-5.4"
	m.agentLoopLastTool = "read_file"
	m.agentLoopLastStatus = "ok"
	m.agentLoopLastDuration = 42

	view := m.renderChatView(140)
	// Header surfaces phase + step + provider/model.
	for _, want := range []string{"tool-result", "2/6", "openai", "gpt-5.4"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected header to contain %q, got:\n%s", want, view)
		}
	}
	// Runtime card adds only what the header lacks: round count and last tool chip.
	for _, want := range []string{"tools 2", "read_file", "42"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected runtime card to contain %q, got:\n%s", want, view)
		}
	}
}

func TestToolTimelineChipsTrackCallAndResult(t *testing.T) {
	m := NewModel(context.Background(), nil)

	m = m.handleEngineEvent(engine.Event{
		Type: "tool:call",
		Payload: map[string]any{
			"tool":           "read_file",
			"step":           1,
			"params_preview": "path=note.txt",
		},
	})
	if len(m.toolTimeline) != 1 || m.toolTimeline[0].Status != "running" {
		t.Fatalf("expected running chip after tool:call, got %#v", m.toolTimeline)
	}

	m = m.handleEngineEvent(engine.Event{
		Type: "tool:result",
		Payload: map[string]any{
			"tool":           "read_file",
			"step":           1,
			"durationMs":     42,
			"success":        true,
			"output_preview": "alpha",
		},
	})
	if len(m.toolTimeline) != 1 {
		t.Fatalf("expected chip to be merged, got %#v", m.toolTimeline)
	}
	chip := m.toolTimeline[0]
	if chip.Status != "ok" || chip.DurationMs != 42 {
		t.Fatalf("expected ok chip with duration, got %#v", chip)
	}

	// After F3 the chip state still drives summaries, but the chat view no
	// longer renders a separate Tool Timeline section — the event lives
	// inline in the transcript instead.
	_ = m.renderChatView(140)
}

func TestChatMentionNavigationAndTabCompletion(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.files = []string{"internal/api/server.go", "internal/app/service.go", "README.md"}
	m.input = "@internal/"

	downModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	down, ok := downModel.(Model)
	if !ok {
		t.Fatalf("expected Model after mention down key, got %T", downModel)
	}
	if down.mentionIndex != 1 {
		t.Fatalf("expected mention index to move to 1, got %d", down.mentionIndex)
	}

	completedModel, _ := down.Update(tea.KeyMsg{Type: tea.KeyTab})
	completed, ok := completedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after mention tab completion, got %T", completedModel)
	}
	if !strings.Contains(completed.input, "[[file:internal/app/service.go]]") {
		t.Fatalf("expected second mention suggestion selected, got %q", completed.input)
	}
}

func TestChatMentionEnterCompletesBeforeSending(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.files = []string{"internal/api/server.go"}
	m.input = "Review @internal/api/ser"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after mention enter completion, got %T", nextModel)
	}
	if !strings.Contains(next.input, "[[file:internal/api/server.go]]") {
		t.Fatalf("expected mention replacement on enter, got %q", next.input)
	}
	if next.sending {
		t.Fatal("expected enter on active mention to complete mention before sending")
	}
	if len(next.transcript) != 0 {
		t.Fatalf("expected no transcript append while mention is being completed, got %#v", next.transcript)
	}
}

func TestChatInputCursorCanEditInMiddle(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.setChatInput("abcd")

	leftModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	left, ok := leftModel.(Model)
	if !ok {
		t.Fatalf("expected Model after left key, got %T", leftModel)
	}
	left2Model, _ := left.Update(tea.KeyMsg{Type: tea.KeyLeft})
	left2, ok := left2Model.(Model)
	if !ok {
		t.Fatalf("expected Model after second left key, got %T", left2Model)
	}

	typedModel, _ := left2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	typed, ok := typedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after middle insert, got %T", typedModel)
	}
	if typed.input != "abXcd" {
		t.Fatalf("expected middle insertion result abXcd, got %q", typed.input)
	}
	if typed.chatCursor != 3 {
		t.Fatalf("expected cursor at 3 after insertion, got %d", typed.chatCursor)
	}
}

func TestChatInputHistoryUpDownNavigation(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0

	m.setChatInput("/help")
	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after first history command, got %T", nextModel)
	}

	next.setChatInput("/context")
	nextModel, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok = nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after second history command, got %T", nextModel)
	}

	next.setChatInput("")
	up1Model, _ := next.Update(tea.KeyMsg{Type: tea.KeyUp})
	up1, ok := up1Model.(Model)
	if !ok {
		t.Fatalf("expected Model after first history up, got %T", up1Model)
	}
	if up1.input != "/context" {
		t.Fatalf("expected latest history command /context, got %q", up1.input)
	}

	up2Model, _ := up1.Update(tea.KeyMsg{Type: tea.KeyUp})
	up2, ok := up2Model.(Model)
	if !ok {
		t.Fatalf("expected Model after second history up, got %T", up2Model)
	}
	if up2.input != "/help" {
		t.Fatalf("expected previous history command /help, got %q", up2.input)
	}

	down1Model, _ := up2.Update(tea.KeyMsg{Type: tea.KeyDown})
	down1, ok := down1Model.(Model)
	if !ok {
		t.Fatalf("expected Model after first history down, got %T", down1Model)
	}
	if down1.input != "/context" {
		t.Fatalf("expected next history command /context, got %q", down1.input)
	}

	down2Model, _ := down1.Update(tea.KeyMsg{Type: tea.KeyDown})
	down2, ok := down2Model.(Model)
	if !ok {
		t.Fatalf("expected Model after second history down, got %T", down2Model)
	}
	if down2.input != "" {
		t.Fatalf("expected restored draft input after history down, got %q", down2.input)
	}
}

func TestChatSlashOperationalCommands(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.latestPatch = "--- a/demo.txt\n+++ b/demo.txt\n@@ -1 +1 @@\n-old\n+new\n"
	m.patchFiles = []string{"demo.txt"}
	m.patchSet = []patchSection{
		{
			Path:      "demo.txt",
			HunkCount: 1,
			Hunks:     []patchHunk{{Header: "@@ -1 +1 @@", Content: "--- a/demo.txt\n+++ b/demo.txt\n@@ -1 +1 @@\n-old\n+new\n"}},
		},
	}

	for _, input := range []string{"/status", "/reload", "/context", "/tools", "/patch"} {
		m.input = input
		nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		next, ok := nextModel.(Model)
		if !ok {
			t.Fatalf("expected Model after %s, got %T", input, nextModel)
		}
		if len(next.transcript) == 0 || next.transcript[len(next.transcript)-1].Role != "system" {
			t.Fatalf("expected system transcript entry after %s, got %#v", input, next.transcript)
		}
		m = next
	}
}

func TestChatSlashUndoCommand(t *testing.T) {
	eng := newTUITestEngine(t)
	eng.ConversationStart()
	eng.Conversation.AddMessage("anthropic", "claude-sonnet-4-6", types.Message{Role: types.RoleUser, Content: "q"})
	eng.Conversation.AddMessage("anthropic", "claude-sonnet-4-6", types.Message{Role: types.RoleAssistant, Content: "a"})

	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.input = "/undo"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after /undo, got %T", nextModel)
	}
	if len(next.transcript) == 0 || !strings.Contains(next.transcript[len(next.transcript)-1].Content, "Undone messages: 2") {
		t.Fatalf("expected undo message in transcript, got %#v", next.transcript)
	}
}

func TestContextCommandSummaryIncludesContextInReport(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.status = engine.Status{
		ContextIn: &engine.ContextInStatus{
			Task:                 "review",
			FileCount:            3,
			TokenCount:           720,
			MaxTokensTotal:       1600,
			MaxTokensPerFile:     500,
			Compression:          "aggressive",
			ExplicitFileMentions: 2,
			Reasons: []string{
				"explicit file markers detected (2), retrieval was narrowed",
				"context budget is near runtime cap; deeper retrieval may require tighter query/file markers",
			},
			Files: []engine.ContextInFileStatus{
				{Path: "internal/engine/engine.go", Score: 7.5, TokenCount: 300},
				{Path: "internal/context/manager.go", Score: 5.1, TokenCount: 220},
			},
		},
	}
	m.pinnedFile = "internal/engine/engine.go"

	summary := m.contextCommandSummary()
	if !strings.Contains(summary, "Last Context In:") {
		t.Fatalf("expected Last Context In in summary, got:\n%s", summary)
	}
	if !strings.Contains(summary, "Why:") || !strings.Contains(summary, "Top files:") {
		t.Fatalf("expected context reasons and top files in summary, got:\n%s", summary)
	}
}

func TestChatSlashContextFullIncludesDetailedFileEvidence(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.status = engine.Status{
		Provider: "openai",
		Model:    "gpt-5.4",
		ContextIn: &engine.ContextInStatus{
			Task:               "review",
			FileCount:          2,
			TokenCount:         580,
			MaxTokensTotal:     1600,
			MaxTokensPerFile:   500,
			ProviderMaxContext: 128000,
			ContextAvailable:   3200,
			Compression:        "standard",
			Reasons: []string{
				"task=review profile(total x1.18, files x1.12, per-file x1.10)",
				"explicit file markers detected (1), retrieval was narrowed",
			},
			Files: []engine.ContextInFileStatus{
				{
					Path:       "internal/engine/engine.go",
					Score:      8.2,
					TokenCount: 320,
					LineStart:  120,
					LineEnd:    220,
					Reason:     "matched query terms and explicit file markers",
				},
			},
		},
	}
	m.input = "/context full"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after /context full, got %T", nextModel)
	}
	if len(next.transcript) == 0 {
		t.Fatalf("expected system transcript after /context full, got %#v", next.transcript)
	}
	last := next.transcript[len(next.transcript)-1]
	if last.Role != "system" {
		t.Fatalf("expected system transcript entry, got %#v", last)
	}
	if !strings.Contains(last.Content, "Context report:") || !strings.Contains(last.Content, "File evidence:") {
		t.Fatalf("expected detailed context report output, got:\n%s", last.Content)
	}
	if !strings.Contains(last.Content, "matched query terms and explicit file markers") {
		t.Fatalf("expected file reason in context report, got:\n%s", last.Content)
	}
}

func TestSetupTabAppliesProviderSelection(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 4
	m.status = eng.Status()
	providers := m.availableProviders()
	if len(providers) < 2 {
		t.Fatalf("expected multiple providers in setup test, got %#v", providers)
	}
	targetIndex := 0
	for i, name := range providers {
		if name == "openai" {
			targetIndex = i
			break
		}
	}
	m.setupIndex = targetIndex

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after setup apply, got %T", nextModel)
	}
	if next.currentProvider() != providers[targetIndex] {
		t.Fatalf("expected setup to apply provider %q, got %q", providers[targetIndex], next.currentProvider())
	}
	if len(next.transcript) == 0 || next.transcript[len(next.transcript)-1].Role != "system" {
		t.Fatalf("expected setup apply to append system transcript, got %#v", next.transcript)
	}
}

func TestSetupTabEditModelAndSave(t *testing.T) {
	eng := newTUITestEngine(t)
	root := t.TempDir()
	eng.ProjectRoot = root

	m := NewModel(context.Background(), eng)
	m.activeTab = 4
	m.status = eng.Status()
	providers := m.availableProviders()
	if len(providers) == 0 {
		t.Fatal("expected providers in setup test")
	}
	targetIndex := 0
	for i, name := range providers {
		if name == "openai" {
			targetIndex = i
			break
		}
	}
	m.setupIndex = targetIndex

	editModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	editing, ok := editModel.(Model)
	if !ok {
		t.Fatalf("expected Model after setup edit key, got %T", editModel)
	}
	if !editing.setupEditing {
		t.Fatal("expected setup editing mode")
	}

	typedModel, _ := editing.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("-dev")})
	typed, ok := typedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after setup draft typing, got %T", typedModel)
	}
	appliedModel, _ := typed.Update(tea.KeyMsg{Type: tea.KeyEnter})
	applied, ok := appliedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after setup draft apply, got %T", appliedModel)
	}
	if got := applied.currentModel(); !strings.Contains(got, "-dev") {
		t.Fatalf("expected edited model suffix in runtime model, got %q", got)
	}

	savedModel, _ := applied.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	saved, ok := savedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after setup save key, got %T", savedModel)
	}
	if !strings.Contains(saved.notice, "Setup saved:") {
		t.Fatalf("expected setup saved notice, got %q", saved.notice)
	}

	path := filepath.Join(root, ".dfmc", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted setup config: %v", err)
	}
	doc := map[string]any{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	providersNode, ok := doc["providers"].(map[string]any)
	if !ok {
		t.Fatalf("expected providers map in setup save, got %#v", doc["providers"])
	}
	profilesNode, ok := providersNode["profiles"].(map[string]any)
	if !ok {
		t.Fatalf("expected profiles map in setup save, got %#v", providersNode["profiles"])
	}
	profileNode, ok := profilesNode["openai"].(map[string]any)
	if !ok {
		t.Fatalf("expected openai profile in setup save, got %#v", profilesNode["openai"])
	}
	gotModel, ok := profileNode["model"].(string)
	if !ok {
		t.Fatalf("expected string model value in setup save, got %#v", profileNode["model"])
	}
	if !strings.Contains(gotModel, "-dev") {
		t.Fatalf("expected edited model persisted, got %#v", gotModel)
	}
}

func TestF6OpensToolsTab(t *testing.T) {
	m := NewModel(context.Background(), nil)

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyF6})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after F6, got %T", nextModel)
	}
	if next.activeTab != 5 {
		t.Fatalf("expected tools tab index 5 after F6, got %d", next.activeTab)
	}
}

func TestCtrlQQuitsProgram(t *testing.T) {
	m := NewModel(context.Background(), nil)
	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if _, ok := nextModel.(Model); !ok {
		t.Fatalf("expected Model return type, got %T", nextModel)
	}
	if cmd == nil {
		t.Fatal("expected quit command for ctrl+q")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg for ctrl+q, got %T", cmd())
	}
}

func TestToolsTabRunsReadFilePreset(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "demo.txt"), "alpha\nbeta\n")

	eng := newTUITestEngine(t)
	eng.ProjectRoot = root

	m := NewModel(context.Background(), eng)
	m.activeTab = 5
	m.status = eng.Status()
	m.files = []string{"demo.txt"}
	m.fileIndex = 0
	m.toolIndex = indexOfString(m.availableTools(), "read_file")
	if m.toolIndex < 0 {
		t.Fatal("expected read_file tool to be registered")
	}

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after tool run key, got %T", nextModel)
	}
	if cmd == nil {
		t.Fatal("expected tool run command")
	}

	msg := cmd()
	finalModel, _ := next.Update(msg)
	final, ok := finalModel.(Model)
	if !ok {
		t.Fatalf("expected Model after tool result, got %T", finalModel)
	}
	if !strings.Contains(final.toolOutput, "alpha") {
		t.Fatalf("expected tool output to contain file content, got:\n%s", final.toolOutput)
	}
	if final.filePath != "demo.txt" {
		t.Fatalf("expected file path to follow tool target, got %q", final.filePath)
	}
}

func TestToolsTabCanEditAndResetParams(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 5
	m.toolIndex = indexOfString(m.availableTools(), "write_file")
	if m.toolIndex < 0 {
		t.Fatal("expected write_file tool to be registered")
	}

	editingModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	editing, ok := editingModel.(Model)
	if !ok {
		t.Fatalf("expected Model after e key, got %T", editingModel)
	}
	if !editing.toolEditing {
		t.Fatal("expected tool editor to open")
	}

	editing.toolDraft = `path=tmp/custom.txt content="hello world" overwrite=true`
	savedModel, _ := editing.Update(tea.KeyMsg{Type: tea.KeyEnter})
	saved, ok := savedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after saving tool params, got %T", savedModel)
	}
	if saved.toolEditing {
		t.Fatal("expected tool editor to close after enter")
	}
	if got := saved.toolOverride(saved.selectedTool()); got != `path=tmp/custom.txt content="hello world" overwrite=true` {
		t.Fatalf("unexpected saved tool params: %q", got)
	}

	resetModel, _ := saved.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	reset, ok := resetModel.(Model)
	if !ok {
		t.Fatalf("expected Model after reset tool params, got %T", resetModel)
	}
	if got := reset.toolOverride(reset.selectedTool()); got != "" {
		t.Fatalf("expected tool params reset, got %q", got)
	}
}

func TestToolsTabAltShortcutOpensEditor(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 5
	m.toolIndex = indexOfString(m.availableTools(), "write_file")
	if m.toolIndex < 0 {
		t.Fatal("expected write_file tool to be registered")
	}

	editingModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune("e")})
	editing, ok := editingModel.(Model)
	if !ok {
		t.Fatalf("expected Model after alt+e key, got %T", editingModel)
	}
	if !editing.toolEditing {
		t.Fatal("expected tool editor to open with alt+e")
	}
}

func TestMutationToolRefreshesPatchAndPreview(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "demo.txt"), "old value\n")
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "dfmc@example.com")
	runGit(t, root, "config", "user.name", "DFMC Test")
	runGit(t, root, "add", "demo.txt")
	runGit(t, root, "commit", "-m", "init")

	eng := newTUITestEngine(t)
	eng.ProjectRoot = root
	if _, err := eng.CallTool(context.Background(), "read_file", map[string]any{
		"path":       "demo.txt",
		"line_start": 1,
		"line_end":   20,
	}); err != nil {
		t.Fatalf("prime read_file: %v", err)
	}

	m := NewModel(context.Background(), eng)
	m.activeTab = 5
	m.status = eng.Status()
	m.files = []string{"demo.txt"}
	m.fileIndex = 0
	m.toolIndex = indexOfString(m.availableTools(), "edit_file")
	if m.toolIndex < 0 {
		t.Fatal("expected edit_file tool to be registered")
	}
	m.toolOverrides["edit_file"] = `path=demo.txt old_string="old value" new_string="new value" replace_all=false`

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after mutation tool run key, got %T", nextModel)
	}
	if cmd == nil {
		t.Fatal("expected mutation tool command")
	}

	msg := cmd()
	finalModel, _ := next.Update(msg)
	final, ok := finalModel.(Model)
	if !ok {
		t.Fatalf("expected Model after mutation tool result, got %T", finalModel)
	}
	if final.activeTab != 3 {
		t.Fatalf("expected mutation tool to switch to Patch tab, got %d", final.activeTab)
	}
	if final.filePath != "demo.txt" {
		t.Fatalf("expected focused file path demo.txt, got %q", final.filePath)
	}
	if !strings.Contains(final.filePreview, "new value") {
		t.Fatalf("expected preview to refresh edited content, got %q", final.filePreview)
	}
	if !containsStringFold(final.changed, "demo.txt") {
		t.Fatalf("expected changed files to include demo.txt, got %#v", final.changed)
	}
	if !strings.Contains(final.diff, "+new value") {
		t.Fatalf("expected worktree diff to refresh edited hunk, got:\n%s", final.diff)
	}
}

func TestToolsTabRunsCommandPreset(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 5
	m.toolIndex = indexOfString(m.availableTools(), "run_command")
	if m.toolIndex < 0 {
		t.Fatal("expected run_command tool to be registered")
	}

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after run_command key, got %T", nextModel)
	}
	if cmd == nil {
		t.Fatal("expected run_command cmd")
	}

	msg := cmd()
	finalModel, _ := next.Update(msg)
	final, ok := finalModel.(Model)
	if !ok {
		t.Fatalf("expected Model after run_command result, got %T", finalModel)
	}
	if !strings.Contains(strings.ToLower(final.toolOutput), "go version") {
		t.Fatalf("expected command output in tools panel, got:\n%s", final.toolOutput)
	}
}

func TestRunCommandSuggestionsPreferGoProjectTargets(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n\ngo 1.23.0\n")
	mustWriteFile(t, filepath.Join(root, "internal", "engine", "engine.go"), "package engine\n")

	eng := newTUITestEngine(t)
	eng.ProjectRoot = root
	m := NewModel(context.Background(), eng)
	m.files = []string{"go.mod", "internal/engine/engine.go"}
	m.fileIndex = 1

	suggestions := m.runCommandSuggestions()
	if len(suggestions) == 0 {
		t.Fatal("expected run command suggestions")
	}
	if !strings.Contains(suggestions[0], `go args="test ./internal/engine -count=1"`) {
		t.Fatalf("expected targeted go test preset first, got %#v", suggestions)
	}
}

func TestLatestAssistantUnifiedDiff(t *testing.T) {
	conv := &conversation.Conversation{
		Branch: "main",
		Branches: map[string][]types.Message{
			"main": {
				{Role: types.RoleUser, Content: "please patch this"},
				{Role: types.RoleAssistant, Content: "```diff\n--- a/demo.txt\n+++ b/demo.txt\n@@ -1 +1 @@\n-old\n+new\n```\n"},
			},
		},
	}

	patch := latestAssistantUnifiedDiff(conv)
	if !strings.Contains(patch, "+++ b/demo.txt") {
		t.Fatalf("expected unified diff, got: %q", patch)
	}
}

func TestExtractPatchedFiles(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git a/internal/auth/service.go b/internal/auth/service.go",
		"--- a/internal/auth/service.go",
		"+++ b/internal/auth/service.go",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"diff --git a/ui/tui/tui.go b/ui/tui/tui.go",
		"--- a/ui/tui/tui.go",
		"+++ b/ui/tui/tui.go",
		"@@ -1 +1 @@",
		"-old",
		"+new",
	}, "\n")

	files := extractPatchedFiles(patch)
	if len(files) != 2 {
		t.Fatalf("expected 2 patched files, got %#v", files)
	}
	if files[0] != "internal/auth/service.go" || files[1] != "ui/tui/tui.go" {
		t.Fatalf("unexpected patched files: %#v", files)
	}
}

func TestParseUnifiedDiffSections(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git a/internal/auth/service.go b/internal/auth/service.go",
		"--- a/internal/auth/service.go",
		"+++ b/internal/auth/service.go",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"diff --git a/ui/tui/tui.go b/ui/tui/tui.go",
		"--- a/ui/tui/tui.go",
		"+++ b/ui/tui/tui.go",
		"@@ -10 +10 @@",
		"-old",
		"+new",
		"@@ -20 +20 @@",
		"-old2",
		"+new2",
	}, "\n")

	sections := parseUnifiedDiffSections(patch)
	if len(sections) != 2 {
		t.Fatalf("expected 2 patch sections, got %#v", sections)
	}
	if sections[0].Path != "internal/auth/service.go" || sections[0].HunkCount != 1 {
		t.Fatalf("unexpected first patch section: %#v", sections[0])
	}
	if sections[1].Path != "ui/tui/tui.go" || sections[1].HunkCount != 2 {
		t.Fatalf("unexpected second patch section: %#v", sections[1])
	}
}

func TestExtractPatchHunks(t *testing.T) {
	diff := strings.Join([]string{
		"--- a/ui/tui/tui.go",
		"+++ b/ui/tui/tui.go",
		"@@ -10 +10 @@",
		"-old",
		"+new",
		"@@ -20 +20 @@",
		"-old2",
		"+new2",
	}, "\n")

	hunks := extractPatchHunks(diff)
	if len(hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %#v", hunks)
	}
	if hunks[0].Header != "@@ -10 +10 @@" {
		t.Fatalf("unexpected first hunk header: %#v", hunks[0])
	}
	if !strings.Contains(hunks[1].Content, "+new2") {
		t.Fatalf("expected second hunk content, got %#v", hunks[1])
	}
}

func TestFilesTabNavigation(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 2
	m.files = []string{"a.go", "b.go", "c.go"}

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after j key, got %T", nextModel)
	}
	if next.fileIndex != 1 {
		t.Fatalf("expected file index 1 after j, got %d", next.fileIndex)
	}

	prevModel, _ := next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	prev, ok := prevModel.(Model)
	if !ok {
		t.Fatalf("expected Model after k key, got %T", prevModel)
	}
	if prev.fileIndex != 0 {
		t.Fatalf("expected file index 0 after k, got %d", prev.fileIndex)
	}
}

func TestFilesTabInsertSelectedFileMarker(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 2
	m.files = []string{"a.go", "b.go"}
	m.fileIndex = 1
	m.input = "please inspect"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after i key, got %T", nextModel)
	}
	if next.activeTab != 0 {
		t.Fatalf("expected active tab 0 after insert, got %d", next.activeTab)
	}
	if !strings.Contains(next.input, "[[file:b.go]]") {
		t.Fatalf("expected input to include selected file marker, got %q", next.input)
	}
}

func TestFilesTabTogglePinnedFile(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 2
	m.files = []string{"a.go", "b.go"}
	m.fileIndex = 1

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after p key, got %T", nextModel)
	}
	if next.pinnedFile != "b.go" {
		t.Fatalf("expected pinned file b.go, got %q", next.pinnedFile)
	}

	clearedModel, _ := next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	cleared, ok := clearedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after second p key, got %T", clearedModel)
	}
	if cleared.pinnedFile != "" {
		t.Fatalf("expected pinned file to clear, got %q", cleared.pinnedFile)
	}
}

func TestFilesTabAltShortcutTogglePinnedFile(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 2
	m.files = []string{"a.go", "b.go"}
	m.fileIndex = 1

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune("p")})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after alt+p key, got %T", nextModel)
	}
	if next.pinnedFile != "b.go" {
		t.Fatalf("expected pinned file b.go via alt+p, got %q", next.pinnedFile)
	}
}

func TestFilesTabPrepareExplainAndReviewPrompts(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 2
	m.files = []string{"internal/auth/service.go"}

	explainModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	explain, ok := explainModel.(Model)
	if !ok {
		t.Fatalf("expected Model after e key, got %T", explainModel)
	}
	if explain.activeTab != 0 {
		t.Fatalf("expected active tab 0 after explain prompt, got %d", explain.activeTab)
	}
	if !strings.Contains(explain.input, "Explain [[file:internal/auth/service.go]]") {
		t.Fatalf("expected explain prompt to target selected file, got %q", explain.input)
	}

	m.activeTab = 2
	reviewModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	review, ok := reviewModel.(Model)
	if !ok {
		t.Fatalf("expected Model after v key, got %T", reviewModel)
	}
	if !strings.Contains(review.input, "Review [[file:internal/auth/service.go]]") {
		t.Fatalf("expected review prompt to target selected file, got %q", review.input)
	}
}

func TestListProjectFilesSkipsIgnoredDirs(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "cmd", "main.go"), "package main\n")
	mustWriteFile(t, filepath.Join(root, ".git", "config"), "[core]\n")
	mustWriteFile(t, filepath.Join(root, "node_modules", "lib.js"), "console.log('x')\n")

	files, err := listProjectFiles(root, 20)
	if err != nil {
		t.Fatalf("listProjectFiles: %v", err)
	}
	if len(files) != 1 || files[0] != "cmd/main.go" {
		t.Fatalf("unexpected files: %#v", files)
	}
}

func TestReadProjectFileRejectsEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWriteFile(t, filepath.Join(outside, "secret.txt"), "nope\n")

	_, _, err := readProjectFile(root, filepath.Join("..", filepath.Base(outside), "secret.txt"), 1024)
	if err == nil {
		t.Fatal("expected escape error")
	}
}

func TestReadProjectFileSkipsBinaryPreview(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bin", "tool.exe")
	mustWriteFileBytes(t, path, []byte{0x4d, 0x5a, 0x90, 0x00, 0x03, 0x00, 0x00, 0x00})

	content, size, err := readProjectFile(root, "bin/tool.exe", 1024)
	if err != nil {
		t.Fatalf("readProjectFile: %v", err)
	}
	if size == 0 {
		t.Fatalf("expected binary file size, got %d", size)
	}
	if !strings.Contains(content, "Binary preview disabled") {
		t.Fatalf("expected binary preview guard message, got %q", content)
	}
}

func TestReadProjectFileSkipsInvalidUTF8Preview(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bin", "blob.dat")
	mustWriteFileBytes(t, path, []byte{0xff, 0xfe, 0xfd, 0xfc, 0xfb})

	content, size, err := readProjectFile(root, "bin/blob.dat", 1024)
	if err != nil {
		t.Fatalf("readProjectFile: %v", err)
	}
	if size == 0 {
		t.Fatalf("expected file size for invalid UTF-8 sample, got %d", size)
	}
	if !strings.Contains(content, "Binary preview disabled") {
		t.Fatalf("expected binary preview guard message, got %q", content)
	}
}

func TestRenderStatusViewIncludesProviderProfileAndModelsDevCache(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.status = engine.Status{
		ProjectRoot: "D:/Codebox/PROJECTS/DFMC",
		Provider:    "anthropic",
		Model:       "claude-sonnet-4-6",
		ProviderProfile: engine.ProviderProfileStatus{
			Name:       "anthropic",
			Model:      "claude-sonnet-4-6",
			Protocol:   "anthropic",
			MaxContext: 1000000,
			MaxTokens:  64000,
			Configured: true,
		},
		ModelsDevCache: engine.ModelsDevCacheStatus{
			Path:      "C:/Users/test/.dfmc/cache/models.dev.json",
			Exists:    true,
			UpdatedAt: time.Date(2026, 4, 14, 10, 30, 0, 0, time.UTC),
			SizeBytes: 2048,
		},
		ContextIn: &engine.ContextInStatus{
			Task:                 "review",
			FileCount:            2,
			TokenCount:           620,
			MaxTokensTotal:       1400,
			MaxTokensPerFile:     500,
			Compression:          "standard",
			ExplicitFileMentions: 1,
			Reasons: []string{
				"task=review profile(total x1.18, files x1.12, per-file x1.10)",
				"explicit file markers detected (1), retrieval was narrowed",
			},
			Files: []engine.ContextInFileStatus{
				{Path: "internal/auth/service.go", Score: 6, TokenCount: 340},
				{Path: "internal/tools/engine.go", Score: 3, TokenCount: 280},
			},
		},
	}

	view := m.renderStatusView(120)
	if !strings.Contains(view, "proto=anthropic") {
		t.Fatalf("expected provider profile in status view, got:\n%s", view)
	}
	if !strings.Contains(view, "Models.dev: ready") {
		t.Fatalf("expected models.dev cache line in status view, got:\n%s", view)
	}
	if !strings.Contains(view, "Runtime:") {
		t.Fatalf("expected runtime connectivity line in status view, got:\n%s", view)
	}
	if !strings.Contains(view, "Context In:") || !strings.Contains(view, "Context Why:") || !strings.Contains(view, "Context Top:") {
		t.Fatalf("expected context-in lines in status view, got:\n%s", view)
	}
}

func TestRenderStatusViewShowsOfflineFallbackHintWhenUnconfigured(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.status = engine.Status{
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
		ProviderProfile: engine.ProviderProfileStatus{
			Name:       "anthropic",
			Model:      "claude-sonnet-4-6",
			Protocol:   "anthropic",
			Configured: false,
		},
	}

	view := m.renderStatusView(120)
	if !strings.Contains(view, "fallback offline") {
		t.Fatalf("expected offline fallback hint in status view, got:\n%s", view)
	}
}

func TestRenderFooterShowsStateAndTabHints(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.status = engine.Status{
		Provider: "openai",
		Model:    "gpt-5.4",
	}
	m.notice = "Agent tool result: read_file (ok, 22ms)"

	footer := m.renderFooter(120)
	if !strings.Contains(footer, "Chat") {
		t.Fatalf("expected tab chip in footer, got:\n%s", footer)
	}
	if !strings.Contains(footer, "Agent tool result") {
		t.Fatalf("expected notice text trailing in footer, got:\n%s", footer)
	}
	if strings.Contains(footer, "\n") {
		t.Fatalf("footer must be a single line, got:\n%s", footer)
	}
	if strings.Contains(footer, "keys:") {
		t.Fatalf("footer must not carry the keys hint — it lives in the ctrl+h overlay now, got:\n%s", footer)
	}
	if strings.Contains(footer, "tab=") || strings.Contains(footer, "provider=") || strings.Contains(footer, "mode=") {
		t.Fatalf("footer should not duplicate header state as key=value, got:\n%s", footer)
	}
}

func TestHelpOverlayShowsTabKeysWhenToggled(t *testing.T) {
	m := NewModel(context.Background(), nil)
	if m.showHelpOverlay {
		t.Fatal("help overlay should default to off")
	}
	m.showHelpOverlay = true
	out := m.renderHelpOverlay(120)
	if !strings.Contains(out, "enter send") || !strings.Contains(out, "ctrl+p palette") {
		t.Fatalf("expected chat hints in help overlay, got:\n%s", out)
	}
	if !strings.Contains(out, "ctrl+h") {
		t.Fatalf("expected self-describing ctrl+h close hint, got:\n%s", out)
	}
}

func TestFitPanelContentHeightTruncatesWithEllipsis(t *testing.T) {
	text := strings.Join([]string{"a", "b", "c", "d", "e"}, "\n")
	got := fitPanelContentHeight(text, 3)
	if strings.Count(got, "\n") != 2 {
		t.Fatalf("expected 3 visible lines after clipping, got:\n%s", got)
	}
	if !strings.Contains(got, "...") {
		t.Fatalf("expected clipped content to include ellipsis line, got:\n%s", got)
	}
}

func TestRenderSetupViewShowsProviderDetails(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 4
	m.status = eng.Status()
	providers := m.availableProviders()
	if len(providers) == 0 {
		t.Fatal("expected providers for setup view")
	}

	view := m.renderSetupView(100)
	if !strings.Contains(view, "Setup") || !strings.Contains(view, "Selection") {
		t.Fatalf("expected setup headings, got:\n%s", view)
	}
	if !strings.Contains(view, "Provider:") || !strings.Contains(view, "Model:") {
		t.Fatalf("expected provider/model details in setup view, got:\n%s", view)
	}
}

func TestRenderToolsViewShowsToolDetails(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 5
	m.toolIndex = indexOfString(m.availableTools(), "read_file")
	m.toolOutput = "Tool: read_file\nSuccess: true\n\npackage main"

	view := m.renderToolsView(100)
	if !strings.Contains(view, "Tools") || !strings.Contains(view, "Tool Detail") {
		t.Fatalf("expected tools headings, got:\n%s", view)
	}
	if !strings.Contains(view, "Description: Read a text file") {
		t.Fatalf("expected tool description in tools view, got:\n%s", view)
	}
	if !strings.Contains(view, "Params:") || !strings.Contains(view, "Last Result") || !strings.Contains(view, "package main") {
		t.Fatalf("expected tool output in tools view, got:\n%s", view)
	}
}

func TestParseToolParamStringSupportsQuotes(t *testing.T) {
	params, err := parseToolParamString(`path=tmp/demo.txt content="hello world" overwrite=true line_end=42`)
	if err != nil {
		t.Fatalf("parseToolParamString: %v", err)
	}
	if got := params["path"]; got != "tmp/demo.txt" {
		t.Fatalf("expected path param, got %#v", got)
	}
	if got := params["content"]; got != "hello world" {
		t.Fatalf("expected quoted content param, got %#v", got)
	}
	if got := params["overwrite"]; got != true {
		t.Fatalf("expected bool coercion, got %#v", got)
	}
	if got := params["line_end"]; got != 42 {
		t.Fatalf("expected int coercion, got %#v", got)
	}
}

func TestComposeChatPromptAvoidsDuplicateFileMarkers(t *testing.T) {
	got := composeChatPrompt("Inspect [[file:cmd/main.go]]", "[[file:cmd/main.go]]")
	if got != "Inspect [[file:cmd/main.go]]" {
		t.Fatalf("expected duplicate marker to be ignored, got %q", got)
	}
}

func TestChatPromptIncludesPinnedFileMarker(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.input = "Please inspect auth flow"
	m.pinnedFile = "internal/auth/service.go"

	got := m.chatPrompt()
	if !strings.Contains(got, "[[file:internal/auth/service.go]]") {
		t.Fatalf("expected pinned file marker in chat prompt, got %q", got)
	}
}

func TestChatPromptExpandsAtMentions(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.input = "Check @internal/config/conf"
	m.files = []string{"internal/config/config.go", "README.md"}

	got := m.chatPrompt()
	if !strings.Contains(got, "[[file:internal/config/config.go]]") {
		t.Fatalf("expected @ mention expansion, got %q", got)
	}
}

func TestFocusChangedFilesPrefersPinnedFile(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.files = []string{"a.go", "b.go", "c.go"}
	m.fileIndex = 0
	m.pinnedFile = "b.go"

	next := m.focusChangedFiles([]string{"c.go", "b.go"})
	if next.fileIndex != 1 {
		t.Fatalf("expected focus to move to pinned changed file, got index %d", next.fileIndex)
	}

	next = m.focusChangedFiles([]string{"c.go"})
	if next.fileIndex != 2 {
		t.Fatalf("expected focus to move to first changed file when pinned file missing, got index %d", next.fileIndex)
	}
}

func TestRenderChatAndFilesViewShowPinnedFile(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.width = 100
	m.pinnedFile = "internal/auth/service.go"
	m.filePath = "internal/auth/service.go"
	m.filePreview = "package auth"
	m.files = []string{"internal/auth/service.go"}

	chatView := m.renderChatView(80)
	if !strings.Contains(chatView, "pinned: [[file:internal/auth/service.go]]") {
		t.Fatalf("expected chat view to show pinned context, got:\n%s", chatView)
	}

	filesView := m.renderFilesView(80)
	if !strings.Contains(filesView, "Pinned for chat context") {
		t.Fatalf("expected files view to show pinned file label, got:\n%s", filesView)
	}
}

func TestAnnotateAssistantPatchAndMarkLatestInTranscript(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.transcript = []chatLine{
		{Role: "user", Content: "patch this"},
		{Role: "assistant", Content: "```diff\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n```\n"},
	}

	m.annotateAssistantPatch(1)
	if len(m.transcript[1].PatchFiles) != 1 || m.transcript[1].PatchFiles[0] != "a.go" {
		t.Fatalf("expected assistant transcript patch files, got %#v", m.transcript[1])
	}
	if m.transcript[1].PatchHunks != 1 {
		t.Fatalf("expected assistant transcript patch hunks, got %#v", m.transcript[1])
	}

	m.markLatestPatchInTranscript("--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new")
	if !m.transcript[1].IsLatestPatch {
		t.Fatalf("expected assistant transcript line to be marked latest: %#v", m.transcript[1])
	}
}

func TestAnnotateAssistantToolUsageFromConversation(t *testing.T) {
	eng := newTUITestEngine(t)
	eng.ConversationStart()
	eng.Conversation.AddMessage("anthropic", "claude-sonnet-4-6", types.Message{
		Role:    types.RoleUser,
		Content: "inspect file",
	})
	eng.Conversation.AddMessage("anthropic", "claude-sonnet-4-6", types.Message{
		Role:    types.RoleAssistant,
		Content: "I inspected the file and ran checks.",
		ToolCalls: []types.ToolCallRecord{
			{Name: "read_file"},
			{Name: "run_command"},
		},
		Results: []types.ToolResultRecord{
			{Name: "read_file", Success: true},
			{Name: "run_command", Success: false},
		},
	})

	m := NewModel(context.Background(), eng)
	m.transcript = []chatLine{
		{Role: "assistant", Content: "I inspected the file and ran checks."},
	}

	m.annotateAssistantToolUsage(0)
	if m.transcript[0].ToolCalls != 2 {
		t.Fatalf("expected 2 tool calls, got %#v", m.transcript[0])
	}
	if m.transcript[0].ToolFailures != 1 {
		t.Fatalf("expected 1 tool failure, got %#v", m.transcript[0])
	}
	if !containsStringFold(m.transcript[0].ToolNames, "read_file") || !containsStringFold(m.transcript[0].ToolNames, "run_command") {
		t.Fatalf("expected tool names in transcript, got %#v", m.transcript[0])
	}
	if summary := m.chatPatchSummary(m.transcript[0]); !strings.Contains(summary, "tools=2") || !strings.Contains(summary, "failures=1") {
		t.Fatalf("expected tool summary in chat summary, got %q", summary)
	}
}

func TestPatchFocusSummaryAndBestTarget(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.files = []string{"a.go", "b.go", "c.go"}
	m.fileIndex = 2
	m.pinnedFile = "b.go"
	m.patchFiles = []string{"a.go", "b.go"}
	m.patchSet = []patchSection{
		{Path: "a.go", HunkCount: 1},
		{Path: "b.go", HunkCount: 2},
	}
	m.patchIndex = 1

	if got := m.bestPatchFileTarget(); got != "b.go" {
		t.Fatalf("expected pinned patch target, got %q", got)
	}
	summary := m.patchFocusSummary()
	if !strings.Contains(summary, "Pinned file is touched") {
		t.Fatalf("expected pinned patch summary, got %q", summary)
	}
}

func TestFocusPatchFileUsesBestTarget(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 3
	m.files = []string{"a.go", "b.go", "c.go"}
	m.fileIndex = 0
	m.pinnedFile = "b.go"
	m.patchFiles = []string{"a.go", "b.go"}
	m.patchSet = []patchSection{
		{Path: "a.go", HunkCount: 1},
		{Path: "b.go", HunkCount: 1},
	}
	m.patchIndex = 1

	nextModel, cmd := m.focusPatchFile()
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model from focusPatchFile, got %T", nextModel)
	}
	if next.activeTab != 2 {
		t.Fatalf("expected focusPatchFile to switch to Files tab, got %d", next.activeTab)
	}
	if next.fileIndex != 1 {
		t.Fatalf("expected focusPatchFile to move to pinned patched file, got index %d", next.fileIndex)
	}
	if cmd == nil {
		t.Fatal("expected preview reload command from focusPatchFile")
	}
}

func TestRenderPatchViewShowsPatchFiles(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.patchFiles = []string{"internal/auth/service.go"}
	m.pinnedFile = "internal/auth/service.go"
	m.latestPatch = "--- a/internal/auth/service.go\n+++ b/internal/auth/service.go\n@@ -1 +1 @@\n-old\n+new\n"
	m.patchSet = []patchSection{
		{
			Path:      "internal/auth/service.go",
			HunkCount: 1,
			Content:   "--- a/internal/auth/service.go\n+++ b/internal/auth/service.go\n@@ -1 +1 @@\n-old\n+new\n",
		},
	}

	view := m.renderPatchView(100)
	if !strings.Contains(view, "Patch Files: internal/auth/service.go") {
		t.Fatalf("expected patch files line in patch view, got:\n%s", view)
	}
	if !strings.Contains(view, "Pinned file is touched by latest patch.") {
		t.Fatalf("expected patch overlap hint in patch view, got:\n%s", view)
	}
}

func TestShiftPatchTargetAndRenderPatchViewUsesCurrentSection(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 3
	m.patchFiles = []string{"a.go", "b.go"}
	m.patchSet = []patchSection{
		{
			Path:      "a.go",
			HunkCount: 1,
			Content:   "--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old-a\n+new-a",
			Hunks:     []patchHunk{{Header: "@@ -1 +1 @@", Content: "--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old-a\n+new-a"}},
		},
		{
			Path:      "b.go",
			HunkCount: 1,
			Content:   "--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-old-b\n+new-b",
			Hunks:     []patchHunk{{Header: "@@ -1 +1 @@", Content: "--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-old-b\n+new-b"}},
		},
	}

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after n key, got %T", nextModel)
	}
	if next.patchIndex != 1 {
		t.Fatalf("expected patch index 1 after navigation, got %d", next.patchIndex)
	}

	view := next.renderPatchView(100)
	if !strings.Contains(view, "Patch Target: b.go (2/2, hunks=1)") {
		t.Fatalf("expected patch target summary for second section, got:\n%s", view)
	}
	if !strings.Contains(view, "Hunk Target: @@ -1 +1 @@ (1/1)") {
		t.Fatalf("expected hunk target summary, got:\n%s", view)
	}
	if !strings.Contains(view, "+++ b/b.go") || strings.Contains(view, "+++ b/a.go") {
		t.Fatalf("expected patch preview to show only current section, got:\n%s", view)
	}
}

func TestShiftPatchHunkAndReviewHints(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 3
	m.patchSet = []patchSection{
		{
			Path:      "internal/auth/service.go",
			HunkCount: 2,
			Hunks: []patchHunk{
				{Header: "@@ -1 +1 @@", Content: "--- a/internal/auth/service.go\n+++ b/internal/auth/service.go\n@@ -1 +1 @@\n-old\n+new"},
				{Header: "@@ -10 +10 @@", Content: "--- a/internal/auth/service.go\n+++ b/internal/auth/service.go\n@@ -10 +10 @@\n-TODO old\n+fmt.Println(\"debug\")"},
			},
		},
	}
	m.patchFiles = []string{"internal/auth/service.go"}

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after j key, got %T", nextModel)
	}
	if next.patchHunk != 1 {
		t.Fatalf("expected patch hunk 1 after navigation, got %d", next.patchHunk)
	}

	view := next.renderPatchView(100)
	if !strings.Contains(view, "Hunk Target: @@ -10 +10 @@ (2/2)") {
		t.Fatalf("expected second hunk target, got:\n%s", view)
	}
	if !strings.Contains(view, "contains TODO/FIXME") || !strings.Contains(view, "check debug or panic statements") {
		t.Fatalf("expected review cues for current hunk, got:\n%s", view)
	}
}

func TestRenderChatViewShowsPatchSummary(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.width = 100
	m.patchSet = []patchSection{{Path: "internal/auth/service.go", HunkCount: 1}}
	m.patchFiles = []string{"internal/auth/service.go"}
	m.transcript = []chatLine{
		{
			Role:          "assistant",
			Content:       "Applied the fix.",
			PatchFiles:    []string{"internal/auth/service.go"},
			PatchHunks:    1,
			IsLatestPatch: true,
		},
	}

	view := m.renderChatView(90)
	if !strings.Contains(view, "patch: internal/auth/service.go | hunks=1 | latest | current target") {
		t.Fatalf("expected patch summary in chat view, got:\n%s", view)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func mustWriteFileBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func newTUITestEngine(t *testing.T) *engine.Engine {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfg := config.DefaultConfig()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("eng.Init: %v", err)
	}
	t.Cleanup(func() { eng.Shutdown() })
	return eng
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

// ---- stats panel ---------------------------------------------------------

func TestRenderStatsPanelShowsAllSections(t *testing.T) {
	info := statsPanelInfo{
		Provider:       "openai",
		Model:          "gpt-5.4",
		Configured:     true,
		ContextTokens:  45_000,
		MaxContext:     200_000,
		AgentActive:    true,
		AgentPhase:     "tool-call",
		AgentStep:      2,
		AgentMaxSteps:  6,
		ToolRounds:     2,
		LastTool:       "read_file",
		LastStatus:     "ok",
		LastDurationMs: 42,
		ToolsEnabled:   true,
		ToolCount:      6,
		Branch:         "main",
		Dirty:          true,
		Inserted:       255,
		Deleted:        10,
		SessionElapsed: 42 * time.Minute,
		MessageCount:   12,
	}
	panel := renderStatsPanel(info, 30)
	for _, want := range []string{
		"PROVIDER", "openai", "gpt-5.4",
		"CONTEXT", "45k/200k",
		"AGENT", "tool-call", "2/6", "read_file", "42ms",
		"TOOLS", "enabled", "6 registered",
		"GIT", "main", "+255", "-10",
		"SESSION", "42m", "12 messages",
		"ctrl+s",
	} {
		if !strings.Contains(panel, want) {
			t.Fatalf("stats panel missing %q, got:\n%s", want, panel)
		}
	}
}

func TestRenderStatsPanelUnconfiguredShowsGuidance(t *testing.T) {
	panel := renderStatsPanel(statsPanelInfo{
		SessionElapsed: 10 * time.Second,
	}, 16)
	for _, want := range []string{"no provider", "f5 setup", "/provider"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("unconfigured stats panel should surface %q, got:\n%s", want, panel)
		}
	}
}

func TestCtrlSTogglesStatsPanel(t *testing.T) {
	m := NewModel(context.Background(), nil)
	if !m.showStatsPanel {
		t.Fatalf("stats panel should default to visible")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	mm := next.(Model)
	if mm.showStatsPanel {
		t.Fatalf("ctrl+s should toggle stats panel off")
	}
	next2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	mm2 := next2.(Model)
	if !mm2.showStatsPanel {
		t.Fatalf("ctrl+s should toggle stats panel back on")
	}
}

func TestStatsPanelHiddenBelowWidthThreshold(t *testing.T) {
	m := NewModel(context.Background(), nil)
	// Below the min width, panel must stay hidden even when the toggle is on.
	if m.statsPanelVisible(statsPanelMinContentWidth - 1) {
		t.Fatalf("panel should hide below width threshold regardless of toggle")
	}
	if !m.statsPanelVisible(statsPanelMinContentWidth) {
		t.Fatalf("panel should show at width threshold")
	}
	m.showStatsPanel = false
	if m.statsPanelVisible(statsPanelMinContentWidth + 40) {
		t.Fatalf("panel must respect the ctrl+s toggle when disabled")
	}
}

func TestChatHeaderSlimDropsPanelOwnedFields(t *testing.T) {
	// When the stats panel is visible the chat header must drop fields the
	// panel owns (provider/model/ctx meter/tools) to avoid double-painting.
	info := chatHeaderInfo{
		Provider:      "openai",
		Model:         "gpt-5.4",
		Configured:    true,
		MaxContext:    200_000,
		ContextTokens: 45_000,
		ToolsEnabled:  true,
		Slim:          true,
	}
	head := renderChatHeader(info, 160)
	for _, leak := range []string{"openai", "gpt-5.4", "tools on", "45,000"} {
		if strings.Contains(head, leak) {
			t.Fatalf("slim header should not repeat panel-owned field %q, got:\n%s", leak, head)
		}
	}

	// Streaming alerts must still surface in the slim header — the panel
	// shows mode too, but the header's job in slim mode is to yell alerts.
	info.Streaming = true
	headStreaming := renderChatHeader(info, 160)
	if !strings.Contains(headStreaming, "streaming") {
		t.Fatalf("slim header should still flag active streaming, got:\n%s", headStreaming)
	}
}
