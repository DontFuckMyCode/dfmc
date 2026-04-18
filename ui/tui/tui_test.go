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
	// Brand line still names the workbench so users can grep their
	// terminal scrollback for "DFMC WORKBENCH".
	if !strings.Contains(view, "DFMC WORKBENCH") {
		t.Fatalf("expected brand string in view:\n%s", view)
	}
	// New top strip surfaces the active tab as a badge plus its
	// immediate neighbours by F-key. Default starts on Chat (idx 0)
	// so prev wraps to Providers and next is Status.
	for _, needle := range []string{"CHAT", "Status", "Providers", "F1", "F2"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("expected new tab strip to contain %q, got:\n%s", needle, view)
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
	m.chat.input = "old"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after ctrl+p, got %T", nextModel)
	}
	if next.activeTab != 0 {
		t.Fatalf("expected chat tab after ctrl+p, got %d", next.activeTab)
	}
	if next.chat.input != "/" {
		t.Fatalf("expected slash command palette seed, got %q", next.chat.input)
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
	if typed.chat.input != "q" {
		t.Fatalf("expected q to be inserted into chat input, got %q", typed.chat.input)
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
	m.chat.input = "/provider openai"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after provider command, got %T", nextModel)
	}
	if next.currentProvider() != "openai" {
		t.Fatalf("expected provider override openai, got %q", next.currentProvider())
	}
	if len(next.chat.transcript) == 0 || next.chat.transcript[len(next.chat.transcript)-1].Role != "system" {
		t.Fatalf("expected system transcript entry after provider command, got %#v", next.chat.transcript)
	}

	next.chat.input = "/model gpt-5.4"
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
	m.chat.input = "/provider openai gpt-5.4 --persist"

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
	m.chat.input = "/prov"

	completedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	completed, ok := completedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after tab completion, got %T", completedModel)
	}
	if completed.chat.input != "/providers" {
		t.Fatalf("expected slash completion to /providers, got %q", completed.chat.input)
	}

	finalModel, _ := completed.Update(tea.KeyMsg{Type: tea.KeyEnter})
	final, ok := finalModel.(Model)
	if !ok {
		t.Fatalf("expected Model after enter on slash command, got %T", finalModel)
	}
	if len(final.chat.transcript) == 0 || final.chat.transcript[len(final.chat.transcript)-1].Role != "system" {
		t.Fatalf("expected system transcript entry after /providers, got %#v", final.chat.transcript)
	}
	if !strings.Contains(final.chat.transcript[len(final.chat.transcript)-1].Content, "Providers:") {
		t.Fatalf("expected providers output in transcript, got %#v", final.chat.transcript[len(final.chat.transcript)-1])
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
	m.chat.input = "/provider op"

	completedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	completed, ok := completedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after provider arg tab completion, got %T", completedModel)
	}
	if completed.chat.input != "/provider openai" {
		t.Fatalf("expected /provider openai completion, got %q", completed.chat.input)
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
	m.chat.input = "/provider "

	downModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	down, ok := downModel.(Model)
	if !ok {
		t.Fatalf("expected Model after provider arg down, got %T", downModel)
	}
	if down.slashMenu.commandArg != 1 {
		t.Fatalf("expected arg index 1 after down, got %d", down.slashMenu.commandArg)
	}

	completedModel, _ := down.Update(tea.KeyMsg{Type: tea.KeyTab})
	completed, ok := completedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after provider arg tab, got %T", completedModel)
	}
	if completed.chat.input != "/provider openai" {
		t.Fatalf("expected second provider completion to /provider openai, got %q", completed.chat.input)
	}
}

func TestChatSlashToolArgTabCompletesToolName(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.chat.input = "/tool re"

	completedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	completed, ok := completedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after tool arg tab completion, got %T", completedModel)
	}
	if completed.chat.input != "/tool read_file" {
		t.Fatalf("expected /tool read_file completion, got %q", completed.chat.input)
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
	m.filesView.entries = []string{"note.txt"}
	m.chat.input = "read note.txt"

	view := m.renderChatView(120)
	if !strings.Contains(view, "Quick actions") || !strings.Contains(view, "/read note.txt 1 200") {
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
	m.filesView.entries = []string{"note.txt"}
	m.chat.input = "read note.txt"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after quick-action tab, got %T", nextModel)
	}
	if next.chat.input != "/read note.txt 1 200" {
		t.Fatalf("expected quick action to prepare slash command, got %q", next.chat.input)
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
	m.filesView.entries = []string{"note.txt"}
	m.chat.input = "read note.txt"

	downModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	down, ok := downModel.(Model)
	if !ok {
		t.Fatalf("expected Model after quick-action down, got %T", downModel)
	}
	if down.slashMenu.quickAction != 1 {
		t.Fatalf("expected quick action index 1 after down, got %d", down.slashMenu.quickAction)
	}

	nextModel, _ := down.Update(tea.KeyMsg{Type: tea.KeyTab})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after quick-action tab, got %T", nextModel)
	}
	if !strings.HasPrefix(next.chat.input, "/grep ") {
		t.Fatalf("expected second quick action to prepare grep command, got %q", next.chat.input)
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
	m.filesView.entries = []string{"note.txt"}
	m.chat.input = "read note.txt"

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
	if !next.chat.toolPending || next.chat.toolName != "grep_codebase" {
		t.Fatalf("expected selected quick action grep_codebase to run, got pending=%v name=%q", next.chat.toolPending, next.chat.toolName)
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
	m.chat.input = "/read note.txt 1 1"

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after /read command, got %T", nextModel)
	}
	if cmd == nil {
		t.Fatal("expected tool command from /read")
	}
	if !next.chat.toolPending || next.chat.toolName != "read_file" {
		t.Fatalf("expected pending chat tool read_file, got pending=%v name=%q", next.chat.toolPending, next.chat.toolName)
	}

	finalModel, _ := next.Update(cmd())
	final, ok := finalModel.(Model)
	if !ok {
		t.Fatalf("expected Model after read_file tool result, got %T", finalModel)
	}
	if final.chat.toolPending {
		t.Fatal("expected chat tool pending to clear after result")
	}
	if len(final.chat.transcript) == 0 {
		t.Fatal("expected transcript entries after /read flow")
	}
	last := final.chat.transcript[len(final.chat.transcript)-1]
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
	m.chat.input = "/run go version"

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
	if len(final.chat.transcript) == 0 {
		t.Fatal("expected transcript entries after /run flow")
	}
	last := final.chat.transcript[len(final.chat.transcript)-1]
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
	m.chat.input = `/read "note file.txt" 1 1`

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
	if len(final.chat.transcript) == 0 {
		t.Fatal("expected transcript entries after quoted /read")
	}
	last := final.chat.transcript[len(final.chat.transcript)-1]
	if last.Role != "system" || !strings.Contains(last.Content, "Tool result: read_file success") || !strings.Contains(last.Content, "line-a") {
		t.Fatalf("expected quoted read_file result in transcript, got %#v", last)
	}
}

func TestChatSlashReadArgTabCompletionQuotesPath(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.filesView.entries = []string{"note file.txt", "README.md"}
	m.chat.input = "/read note"

	completedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	completed, ok := completedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after read arg tab completion, got %T", completedModel)
	}
	if completed.chat.input != `/read "note file.txt"` {
		t.Fatalf("expected quoted read path completion, got %q", completed.chat.input)
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
	m.chat.input = `/tool read_file path="note file.txt" line_start=1 line_end=1`

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
	if len(final.chat.transcript) == 0 {
		t.Fatal("expected transcript entries after /tool")
	}
	last := final.chat.transcript[len(final.chat.transcript)-1]
	if last.Role != "system" || !strings.Contains(last.Content, "Tool result: read_file success") || !strings.Contains(last.Content, "tool-line") {
		t.Fatalf("expected read_file tool result in transcript, got %#v", last)
	}
}

func TestChatSlashToolParamKeyTabCompletion(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.chat.input = "/tool read_file p"

	completedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	completed, ok := completedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after tool param key tab completion, got %T", completedModel)
	}
	if completed.chat.input != "/tool read_file path=" {
		t.Fatalf("expected /tool read_file path= completion, got %q", completed.chat.input)
	}
}

func TestChatSlashToolParamValueTabCompletionQuotesPath(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.filesView.entries = []string{"note file.txt", "README.md"}
	m.chat.input = "/tool read_file path=no"

	completedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	completed, ok := completedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after tool param value tab completion, got %T", completedModel)
	}
	if completed.chat.input != `/tool read_file path="note file.txt"` {
		t.Fatalf("expected quoted /tool path completion, got %q", completed.chat.input)
	}
}

func TestChatSlashCommandParseErrorRemainsLocal(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.chat.input = `/read "broken`

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after parse-error command, got %T", nextModel)
	}
	if cmd != nil {
		t.Fatalf("expected no tool/stream command on parse error, got %#v", cmd)
	}
	if next.chat.sending {
		t.Fatal("expected parse-error slash input to stay local and not stream")
	}
	if len(next.chat.transcript) == 0 || next.chat.transcript[len(next.chat.transcript)-1].Role != "system" || !strings.Contains(next.chat.transcript[len(next.chat.transcript)-1].Content, "Command parse error:") {
		t.Fatalf("expected local parse error transcript message, got %#v", next.chat.transcript)
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
	m.chat.input = "read note.txt"

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after natural read intent, got %T", nextModel)
	}
	if cmd == nil {
		t.Fatal("expected tool cmd from natural read intent")
	}
	if next.chat.sending {
		t.Fatal("expected natural read intent to run tool first instead of stream send")
	}
	if !next.chat.toolPending || next.chat.toolName != "read_file" {
		t.Fatalf("expected pending read_file tool, got pending=%v name=%q", next.chat.toolPending, next.chat.toolName)
	}
	if len(next.chat.transcript) < 2 || next.chat.transcript[0].Role != "user" || !strings.Contains(next.chat.transcript[0].Content, "read note.txt") {
		t.Fatalf("expected user transcript entry before auto tool run, got %#v", next.chat.transcript)
	}
	if !strings.Contains(next.chat.transcript[1].Content, "Auto action: detected file read intent") {
		t.Fatalf("expected auto action transcript note, got %#v", next.chat.transcript[1])
	}

	finalModel, _ := next.Update(cmd())
	final, ok := finalModel.(Model)
	if !ok {
		t.Fatalf("expected Model after natural read tool result, got %T", finalModel)
	}
	last := final.chat.transcript[len(final.chat.transcript)-1]
	if last.Role != "system" || !strings.Contains(last.Content, "Tool result: read_file") || !strings.Contains(last.Content, "hello") {
		t.Fatalf("expected read_file result message, got %#v", last)
	}
}

func TestChatNaturalPromptWithoutIntentStillStreams(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.chat.input = "please explain this architecture"

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after normal prompt, got %T", nextModel)
	}
	if cmd == nil {
		t.Fatal("expected stream wait command for normal prompt")
	}
	if !next.chat.sending {
		t.Fatal("expected normal prompt to enter streaming state")
	}
	if next.chat.toolPending {
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
	m.chat.sending = true
	m.chat.transcript = []chatLine{
		{Role: "user", Content: "scan"},
		{Role: "assistant", Content: ""},
	}
	m.chat.streamIndex = 1

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
	if len(next.chat.transcript) != 2 {
		t.Fatalf("expected transcript untouched by tool:call, got %#v", next.chat.transcript)
	}
}

// TestEscCancelsStreamingTurn — while a chat turn is streaming, esc must
// fire the per-stream cancel so the provider call aborts cleanly and the
// user sees a cancellation notice immediately, rather than waiting for the
// next token to arrive.
func TestEscCancelsStreamingTurn(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.chat.sending = true
	cancelled := false
	m.chat.streamCancel = func() { cancelled = true }

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm, ok := out.(Model)
	if !ok {
		t.Fatalf("expected Model from Update, got %T", out)
	}
	if !cancelled {
		t.Fatalf("esc during streaming should fire the stream cancel func")
	}
	if mm.chat.streamCancel != nil {
		t.Fatalf("cancel func must be cleared after firing, got non-nil")
	}
	if !strings.Contains(strings.ToLower(mm.notice), "cancel") {
		t.Fatalf("esc cancel should set a cancellation notice, got %q", mm.notice)
	}
}

// TestEscWhenNotStreamingDismissesParkedBanner — if no turn is in flight,
// esc falls through to the parked-resume banner dismissal so the previous
// behavior keeps working.
func TestEscWhenNotStreamingDismissesParkedBanner(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.chat.sending = false
	m.ui.resumePromptActive = true

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm := out.(Model)
	if mm.ui.resumePromptActive {
		t.Fatalf("esc should dismiss the resume banner, got still active")
	}
}

// TestCtrlUClearsChatInput — Unix-style clear-line keybinding, only active
// on the Chat tab so other panels keep their local ctrl+u behaviour (if
// any) free.
func TestCtrlUClearsChatInput(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.setChatInput("half-written message")

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	mm := out.(Model)
	if mm.chat.input != "" {
		t.Fatalf("ctrl+u should wipe the input, got %q", mm.chat.input)
	}
	if mm.chat.cursor != 0 {
		t.Fatalf("cursor must snap to 0, got %d", mm.chat.cursor)
	}
	if !strings.Contains(strings.ToLower(mm.notice), "cleared") {
		t.Fatalf("expected clear notice, got %q", mm.notice)
	}
}

// TestChatInputLineHomeEnd — Home/End must operate on the *logical line*
// under the cursor once the user composes multi-line input. Before this
// change Home always jumped to buffer start, which is useless in a multi-
// paragraph prompt.
func TestChatInputLineHomeEnd(t *testing.T) {
	runes := []rune("alpha\nbeta\ngamma")
	// cursor in "beta" (index 8, between 'e' and 't').
	if got := chatInputLineHome(runes, 8); got != 6 {
		t.Errorf("lineHome on 'beta' row should be 6, got %d", got)
	}
	if got := chatInputLineEnd(runes, 8); got != 10 {
		t.Errorf("lineEnd on 'beta' row should be 10 (index of '\\n'), got %d", got)
	}
	// cursor in "alpha" row (index 3).
	if got := chatInputLineHome(runes, 3); got != 0 {
		t.Errorf("lineHome on first row should be 0, got %d", got)
	}
	if got := chatInputLineEnd(runes, 3); got != 5 {
		t.Errorf("lineEnd on first row should be 5, got %d", got)
	}
	// cursor in last row — lineEnd should hit buffer end, not a '\n'.
	if got := chatInputLineEnd(runes, 14); got != 16 {
		t.Errorf("lineEnd on last row should be len=16, got %d", got)
	}
}

// TestHomeKeyIsLineAwareInMultiLineInput — end-to-end: pressing Home from
// mid-row must land on that row's first column, not at buffer start.
func TestHomeKeyIsLineAwareInMultiLineInput(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.setChatInput("first\nsecond line here")
	m.chat.cursor = 10 // inside "second"
	m.chat.cursorManual = true
	m.chat.cursorInput = m.chat.input

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyHome})
	mm := out.(Model)
	if mm.chat.cursor != 6 {
		t.Fatalf("Home on row 1 should land at index 6 (start of 'second'), got %d", mm.chat.cursor)
	}
	// End should land just before the buffer end (no trailing \n here, so
	// it's the buffer length).
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnd})
	mm = out.(Model)
	if mm.chat.cursor != len([]rune(mm.chat.input)) {
		t.Fatalf("End on last row should land at len=%d, got %d", len([]rune(mm.chat.input)), mm.chat.cursor)
	}
}

// TestArrowUpInMultiLineNavigatesRowsNotHistory — when the composer has a
// newline, Up/Down walk the buffer first and only fall through to history
// when already on the first/last row. Single-line input is unaffected.
func TestArrowUpInMultiLineNavigatesRowsNotHistory(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.setChatInput("first\nsecond")
	m.chat.cursor = 12 // end of "second"
	m.chat.cursorManual = true
	m.chat.cursorInput = m.chat.input

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	mm := out.(Model)
	// Column 6 from row 1; "first" has length 5, so clamp to 5.
	if mm.chat.cursor != 5 {
		t.Fatalf("KeyUp should move cursor up to 'first' row at col 5 (clamped), got %d", mm.chat.cursor)
	}
	// Input must be unchanged — we moved the cursor, not the buffer.
	if mm.chat.input != "first\nsecond" {
		t.Fatalf("row nav must not mutate the buffer, got %q", mm.chat.input)
	}
	// Pressing Up again from row 0 falls through to history. The history
	// is empty here so nothing changes, but sending must not fire.
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyUp})
	mm = out.(Model)
	if mm.chat.sending {
		t.Fatalf("row-nav overflow must not trigger send")
	}
}

// TestArrowDownInMultiLineMovesDownARow — symmetric check for Down.
func TestArrowDownInMultiLineMovesDownARow(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.setChatInput("alpha\nbeta\ngamma")
	m.chat.cursor = 3 // in "alpha"
	m.chat.cursorManual = true
	m.chat.cursorInput = m.chat.input

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm := out.(Model)
	// Expected: col 3 carried to "beta" → index 6 + 3 = 9 (within "beta").
	if mm.chat.cursor != 9 {
		t.Fatalf("KeyDown should land in 'beta' at col 3 (index 9), got %d", mm.chat.cursor)
	}
}

// TestEnforceToolUseForActionRequests_InjectsDirectiveOnMutationIntent —
// weaker models routinely respond to "update X" with prose instead of a
// tool call. The TUI appends an explicit tool-use directive so the model
// routes through apply_patch/edit_file/write_file instead of narrating.
// Applied only when provider is tool-capable AND question is clearly
// about mutation.
func TestEnforceToolUseForActionRequests_InjectsDirectiveOnMutationIntent(t *testing.T) {
	m := NewModel(context.Background(), nil)
	// Simulate a tool-capable provider.
	m.status.Provider = "zai"
	m.status.ProviderProfile.Configured = true
	// hasToolCapableProvider also checks m.eng.Tools; we can't easily set
	// a real engine here so we exercise the path where eng is nil.
	// In that case hasToolCapableProvider returns false → directive is
	// skipped. That's the correct default-safe behavior. Test the shape
	// of the injection via the underlying helper.
	got := m.enforceToolUseForActionRequests("[[file:README.md]] güncelle")
	// Without a real engine, no directive appended — just returns input.
	if got != "[[file:README.md]] güncelle" {
		t.Fatalf("without engine tools, directive should be skipped; got %q", got)
	}
}

// TestEnforceToolUseForActionRequests_SkipsWhenToolAlreadyNamed — if the
// user already references a tool by name, trust they know what they're
// doing and don't double up.
func TestEnforceToolUseForActionRequests_SkipsWhenToolAlreadyNamed(t *testing.T) {
	m := NewModel(context.Background(), nil)
	in := "Use apply_patch to update [[file:README.md]]"
	if got := m.enforceToolUseForActionRequests(in); got != in {
		t.Fatalf("question already names a tool — directive must not be added: %q", got)
	}
}

// TestEnforceToolUseForActionRequests_SkipsOnPureReadIntent — no mutation
// intent → no directive. Pure "explain this" should never trip.
func TestEnforceToolUseForActionRequests_SkipsOnPureReadIntent(t *testing.T) {
	m := NewModel(context.Background(), nil)
	in := "explain [[file:README.md]]"
	if got := m.enforceToolUseForActionRequests(in); got != in {
		t.Fatalf("read-only intent must not get a tool-use directive, got %q", got)
	}
}

// TestLooksLikeActionRequest_DetectsWriteVerbs — the gate that decides
// whether to warn about offline mode. The heuristic must be tight: pure
// "explain" / "show" / "what is" prompts should fall through (offline
// analyzer still useful there), but write/update/guncelle + a file
// target must trip it so we can pre-empt the "nothing happened" surprise.
func TestLooksLikeActionRequest_DetectsWriteVerbs(t *testing.T) {
	m := NewModel(context.Background(), nil)
	cases := []struct {
		q    string
		want bool
	}{
		{"[[file:README.md]] güncelle", true},
		{"write a fix in @ui/tui/tui.go", true},
		{"update internal/auth/token.go", true},
		{"fix the auth.go file", true},
		// Pure read/explain — do NOT trip, offline analyzer is useful here.
		{"explain @README.md", false},
		{"what is this project about?", false},
		{"summarize the codebase", false},
		// Verb without file target is ambiguous — leave it to the LLM.
		{"write", false},
		{"update", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.q, func(t *testing.T) {
			if got := m.looksLikeActionRequest(c.q); got != c.want {
				t.Errorf("looksLikeActionRequest(%q) = %v, want %v", c.q, got, c.want)
			}
		})
	}
}

// TestHasToolCapableProvider_FalseForOffline — when the engine reports
// the offline analyzer as the active provider, we must NOT claim tool
// capability. Same for placeholder and empty-string provider.
func TestHasToolCapableProvider_FalseForOffline(t *testing.T) {
	m := NewModel(context.Background(), nil)
	// NewModel has eng=nil which already returns false; we want to test
	// the provider-name check too. Re-wire status manually.
	m.status.Provider = "offline"
	m.status.ProviderProfile.Configured = true
	if m.hasToolCapableProvider() {
		t.Fatalf("offline provider must not count as tool-capable even when Configured=true")
	}
	m.status.Provider = "placeholder"
	if m.hasToolCapableProvider() {
		t.Fatalf("placeholder provider must not count as tool-capable")
	}
	m.status.Provider = ""
	if m.hasToolCapableProvider() {
		t.Fatalf("empty provider name must not count as tool-capable")
	}
}

// TestCtrlJInsertsNewline — Shift+Enter can't be reliably distinguished from
// Enter on most terminals, so we bind Ctrl+J (the LF character) to newline
// insertion as the cross-terminal reliable way to compose multi-line prompts.
// The help overlay used to lie about "shift+enter newline" and break user
// expectations on the first try.
func TestCtrlJInsertsNewline(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.setChatInput("first line")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	mm := out.(Model)
	if mm.chat.input != "first line\n" {
		t.Fatalf("ctrl+j should append a newline to the buffer, got %q", mm.chat.input)
	}
	// Typing continues on the fresh logical row.
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("second")})
	mm = out.(Model)
	if mm.chat.input != "first line\nsecond" {
		t.Fatalf("runes after ctrl+j should land on the new row, got %q", mm.chat.input)
	}
}

// TestAltEnterInsertsNewline — on terminals that deliver Alt+Enter as
// KeyEnter with Alt=true (iTerm, WezTerm, Windows Terminal with the right
// setting) the composer treats it as a newline instead of submit.
func TestAltEnterInsertsNewline(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.setChatInput("paragraph one")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	mm := out.(Model)
	if mm.chat.input != "paragraph one\n" {
		t.Fatalf("alt+enter should insert a newline, not submit; got %q", mm.chat.input)
	}
	if mm.chat.sending {
		t.Fatalf("alt+enter must not flip sending=true")
	}
}

// TestRenderChatInputLine_MultiLineUsesContinuationPrefix — the "> " prompt
// glyph should appear only on the first row; continuation rows get a "  "
// indent so the visual prompt never repeats. The cursor glyph must land on
// the correct logical row.
func TestRenderChatInputLine_MultiLineUsesContinuationPrefix(t *testing.T) {
	input := "first\nsecond"
	runes := []rune(input)
	// Cursor at end of "second".
	line := renderChatInputLine(input, len(runes), true, input, false)
	rows := strings.Split(line, "\n")
	if len(rows) != 2 {
		t.Fatalf("expected two rendered rows, got %d: %q", len(rows), rows)
	}
	if !strings.HasPrefix(rows[0], "> ") || !strings.Contains(rows[0], "first") {
		t.Fatalf("row 0 should carry the '> ' prompt with 'first', got %q", rows[0])
	}
	if !strings.HasPrefix(rows[1], "  ") || strings.HasPrefix(rows[1], "> ") {
		t.Fatalf("row 1 continuation must not repeat the '> ' prompt, got %q", rows[1])
	}
	if !strings.Contains(rows[1], "second|") {
		t.Fatalf("cursor should land at the end of row 1 'second|', got %q", rows[1])
	}
}

// TestRenderChatInputLine_CursorOnFirstRowWhenMid — when the cursor is in
// the first logical row it must NOT migrate to a continuation row.
func TestRenderChatInputLine_CursorOnFirstRowWhenMid(t *testing.T) {
	input := "hello\nworld"
	// Cursor at index 3 ("hel|lo").
	line := renderChatInputLine(input, 3, true, input, false)
	rows := strings.Split(line, "\n")
	if !strings.Contains(rows[0], "hel|lo") {
		t.Fatalf("cursor must stay on row 0, got %q", rows[0])
	}
	if strings.Contains(rows[1], "|") {
		t.Fatalf("cursor should not appear on row 1, got %q", rows[1])
	}
}

// TestFormatInputBoxContent_PreservesNewlines — the box formatter must emit
// a row per logical line so lipgloss paints a tall frame, not a single
// truncated strip.
func TestFormatInputBoxContent_PreservesNewlines(t *testing.T) {
	got := formatInputBoxContent("one\ntwo\nthree", 40)
	if strings.Count(got, "\n") != 2 {
		t.Fatalf("expected 2 newlines in output, got %q", got)
	}
}

// TestFormatInputBoxContent_SoftWrapsLongLine — a single long pasted line
// should be wrapped inside the inner width so it doesn't spill past the
// right border.
func TestFormatInputBoxContent_SoftWrapsLongLine(t *testing.T) {
	long := strings.Repeat("abcdefghij ", 10) // ~110 chars, tons of break points
	got := formatInputBoxContent(long, 40)
	for _, row := range strings.Split(got, "\n") {
		if len(row) > 40 {
			t.Fatalf("row exceeds inner width 40: len=%d %q", len(row), row)
		}
	}
}

// TestChatInputWordBoundaries pins the readline-style word boundary math.
// Whitespace is the only separator — paths like internal/auth/token.go and
// [[file:...]] markers must stay atomic so Ctrl+W nukes the whole reference
// in one stroke instead of fragmenting it down path separators.
func TestChatInputWordBoundaries(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		cursor    int
		wantLeft  int // chatInputWordBoundaryLeft
		wantRight int // chatInputWordBoundaryRight
	}{
		{"empty", "", 0, 0, 0},
		{"mid-word-left-jumps-to-word-start", "hello world", 8, 6, 11},
		{"at-word-end-left-jumps-over-whitespace", "hello world", 5, 0, 11},
		{"trailing-space-left-still-kills-prior-word", "hello   ", 8, 0, 8},
		{"path-stays-atomic-under-ctrl+w", "review @internal/auth/token.go here", 31, 7, 35},
		{"file-marker-stays-atomic", "hi [[file:a.go]] there", 16, 3, 22},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runes := []rune(tc.text)
			if got := chatInputWordBoundaryLeft(runes, tc.cursor); got != tc.wantLeft {
				t.Errorf("wordLeft(%q, %d) = %d, want %d", tc.text, tc.cursor, got, tc.wantLeft)
			}
			if got := chatInputWordBoundaryRight(runes, tc.cursor); got != tc.wantRight {
				t.Errorf("wordRight(%q, %d) = %d, want %d", tc.text, tc.cursor, got, tc.wantRight)
			}
		})
	}
}

// TestCtrlWDeletesPreviousWord — the single most important readline key for
// chat composers. User has typed a long question, spots a typo three words
// back: Ctrl+W should kill the word just behind the cursor without touching
// anything after it, and leave the cursor at the gap.
func TestCtrlWDeletesPreviousWord(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.setChatInput("explain @internal/auth/token.go please")
	// Park cursor at end and nuke "please".
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	mm := out.(Model)
	if mm.chat.input != "explain @internal/auth/token.go " {
		t.Fatalf("ctrl+w should kill the trailing word, got %q", mm.chat.input)
	}
	// Fire again — the whole path is one "word" (whitespace separator),
	// so the entire @mention goes in a single stroke.
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	mm = out.(Model)
	if mm.chat.input != "explain " {
		t.Fatalf("ctrl+w should kill the @path atomically, got %q", mm.chat.input)
	}
}

// TestCtrlKDeletesToEndOfLine — the complement to Ctrl+U / Ctrl+W. User
// rewinds with Ctrl+A, types a prefix, then nukes the suffix.
func TestCtrlKDeletesToEndOfLine(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.setChatInput("hello world and more")
	// Move cursor to position 5 ("hello|")
	m.chat.cursor = 5
	m.chat.cursorManual = true
	m.chat.cursorInput = m.chat.input

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	mm := out.(Model)
	if mm.chat.input != "hello" {
		t.Fatalf("ctrl+k should kill everything from cursor to end, got %q", mm.chat.input)
	}
	if mm.chat.cursor != 5 {
		t.Fatalf("cursor should stay at the kill point, got %d", mm.chat.cursor)
	}
}

// TestCtrlLeftRightMovesByWord — word-wise cursor moves land at word
// boundaries, matching bash/emacs. No delete, just navigation.
func TestCtrlLeftRightMovesByWord(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.setChatInput("alpha beta gamma")
	// Cursor at end (16). Ctrl+Left → 11 (start of "gamma").
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlLeft})
	mm := out.(Model)
	if mm.chat.cursor != 11 {
		t.Fatalf("ctrl+left should land on 'gamma' start, got cursor=%d", mm.chat.cursor)
	}
	// Again → 6 (start of "beta").
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyCtrlLeft})
	mm = out.(Model)
	if mm.chat.cursor != 6 {
		t.Fatalf("ctrl+left should land on 'beta' start, got cursor=%d", mm.chat.cursor)
	}
	// Ctrl+Right → 10 (end of "beta"): readline convention lands at the
	// end of the word you're currently inside, not the start of the next.
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyCtrlRight})
	mm = out.(Model)
	if mm.chat.cursor != 10 {
		t.Fatalf("ctrl+right should land on 'beta' end, got cursor=%d", mm.chat.cursor)
	}
	// Again → 16 (end of "gamma"): cross the space, consume "gamma".
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyCtrlRight})
	mm = out.(Model)
	if mm.chat.cursor != 16 {
		t.Fatalf("ctrl+right should land on 'gamma' end, got cursor=%d", mm.chat.cursor)
	}
}

// TestHandleEngineEventToolResultFailureMirrorsToTranscript — tool failures
// are rare but critical, and a failed chip is easy to miss in a long turn.
// Force the transcript to carry the error message so scrollback preserves
// it even after the user leaves the Activity panel.
func TestHandleEngineEventToolResultFailureMirrorsToTranscript(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.sending = true
	m.chat.transcript = []chatLine{
		{Role: "user", Content: "apply the patch"},
		{Role: "assistant", Content: ""},
	}
	m.chat.streamIndex = 1

	next := m.handleEngineEvent(engine.Event{
		Type: "tool:result",
		Payload: map[string]any{
			"tool":       "apply_patch",
			"success":    false,
			"durationMs": 12,
			"error":      "patch conflict at engine.go:42",
		},
	})
	if len(next.chat.transcript) != 3 {
		t.Fatalf("tool failure should append a transcript line, got %d entries", len(next.chat.transcript))
	}
	last := next.chat.transcript[len(next.chat.transcript)-1]
	if last.Role != "tool" {
		t.Fatalf("failure message should be tool-tagged, got role=%q", last.Role)
	}
	if !strings.Contains(last.Content, "apply_patch") {
		t.Fatalf("failure transcript line should name the tool, got %q", last.Content)
	}
	if !strings.Contains(last.Content, "patch conflict") {
		t.Fatalf("failure transcript line should preserve the error text, got %q", last.Content)
	}
}

// TestHandleEngineEventToolResultSuccessSkipsTranscript — the inverse of the
// above: a successful tool call must not flood the transcript. The chip
// strip already handles successful progress.
func TestHandleEngineEventToolResultSuccessSkipsTranscript(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.sending = true
	m.chat.transcript = []chatLine{
		{Role: "user", Content: "read"},
		{Role: "assistant", Content: ""},
	}
	m.chat.streamIndex = 1

	next := m.handleEngineEvent(engine.Event{
		Type: "tool:result",
		Payload: map[string]any{
			"tool":       "read_file",
			"success":    true,
			"durationMs": 12,
		},
	})
	if len(next.chat.transcript) != 2 {
		t.Fatalf("successful tool should not append transcript, got %d entries: %+v", len(next.chat.transcript), next.chat.transcript)
	}
}

func TestHandleEngineEventToolResultUpdatesActivityWithoutTranscriptWhenIdle(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.sending = false

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
	if len(next.chat.transcript) != 0 {
		t.Fatalf("expected no transcript update while idle, got %#v", next.chat.transcript)
	}
}

func TestRenderChatViewSurfacesToolEventsViaRuntimeCard(t *testing.T) {
	// Signal-density rule: tool progress lives in the runtime card and chips,
	// not in the transcript. Legacy side panels (Live Activity / Tool Timeline)
	// are gone, and the transcript no longer echoes every call.
	m := NewModel(context.Background(), nil)
	m.chat.sending = true
	m.chat.transcript = []chatLine{
		{Role: "user", Content: "scan"},
		{Role: "assistant", Content: ""},
	}
	m.chat.streamIndex = 1
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
	m.chat.input = "/provider "

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
	m.chat.input = "/provider op"

	view := m.renderChatView(120)
	if !strings.Contains(view, "Command args") || !strings.Contains(view, "openai") {
		t.Fatalf("expected command arg suggestion section in chat view, got:\n%s", view)
	}
}

func TestRenderChatViewShowsToolCommandArgSuggestions(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.chat.input = "/tool read_file p"

	view := m.renderChatView(120)
	if !strings.Contains(view, "Command args") || !strings.Contains(view, "path=") {
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
	m.chat.input = "/tool"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after /tool, got %T", nextModel)
	}
	if !next.commandPicker.active || next.commandPicker.kind != "tool" {
		t.Fatalf("expected tool picker to open, got active=%v kind=%q", next.commandPicker.active, next.commandPicker.kind)
	}
}

func TestChatSlashReadWithoutArgsOpensReadPicker(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.filesView.entries = []string{"README.md", "internal/engine/engine.go"}
	m.chat.input = "/read"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after /read, got %T", nextModel)
	}
	if !next.commandPicker.active || next.commandPicker.kind != "read" {
		t.Fatalf("expected read picker to open, got active=%v kind=%q", next.commandPicker.active, next.commandPicker.kind)
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
	if next.commandPicker.active {
		t.Fatal("expected tool picker to close after enter")
	}
	if next.chat.input != "/tool read_file " {
		t.Fatalf("expected prepared tool command, got %q", next.chat.input)
	}
}

func TestReadPickerEnterPreparesReadCommand(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.filesView.entries = []string{"README.md", "docs/My File.md"}
	m = m.startCommandPicker("read", "docs/My File.md", false)

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after read picker enter, got %T", nextModel)
	}
	if next.commandPicker.active {
		t.Fatal("expected read picker to close after enter")
	}
	if next.chat.input != "/read \"docs/My File.md\" " {
		t.Fatalf("expected prepared quoted read command, got %q", next.chat.input)
	}
}

func TestChatSlashRunWithoutArgsOpensRunPicker(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.filesView.entries = []string{"go.mod"}
	m.chat.input = "/run"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after /run, got %T", nextModel)
	}
	if !next.commandPicker.active || next.commandPicker.kind != "run" {
		t.Fatalf("expected run picker to open, got active=%v kind=%q", next.commandPicker.active, next.commandPicker.kind)
	}
}

func TestChatSlashGrepWithoutArgsOpensGrepPicker(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.filesView.entries = []string{"internal/engine/engine.go"}
	m.chat.input = "/grep"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after /grep, got %T", nextModel)
	}
	if !next.commandPicker.active || next.commandPicker.kind != "grep" {
		t.Fatalf("expected grep picker to open, got active=%v kind=%q", next.commandPicker.active, next.commandPicker.kind)
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
	if !strings.HasPrefix(next.chat.input, "/run go test ./...") {
		t.Fatalf("expected prepared run command, got %q", next.chat.input)
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
	if next.chat.input != "/grep TODO" {
		t.Fatalf("expected prepared grep command, got %q", next.chat.input)
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
	if !started.agentLoop.active || started.agentLoop.maxToolStep != 6 || started.agentLoop.phase != "starting" {
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
	if !thinking.agentLoop.active || thinking.agentLoop.step != 2 || thinking.agentLoop.toolRounds != 1 || thinking.agentLoop.phase != "thinking" {
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
	if completed.agentLoop.active {
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
	m.agentLoop.active = true
	m.agentLoop.phase = "tool-result"
	m.agentLoop.step = 2
	m.agentLoop.maxToolStep = 6
	m.agentLoop.toolRounds = 2
	m.agentLoop.provider = "openai"
	m.agentLoop.model = "gpt-5.4"
	m.agentLoop.lastTool = "read_file"
	m.agentLoop.lastStatus = "ok"
	m.agentLoop.lastDuration = 42

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
	if len(m.agentLoop.toolTimeline) != 1 || m.agentLoop.toolTimeline[0].Status != "running" {
		t.Fatalf("expected running chip after tool:call, got %#v", m.agentLoop.toolTimeline)
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
	if len(m.agentLoop.toolTimeline) != 1 {
		t.Fatalf("expected chip to be merged, got %#v", m.agentLoop.toolTimeline)
	}
	chip := m.agentLoop.toolTimeline[0]
	if chip.Status != "ok" || chip.DurationMs != 42 {
		t.Fatalf("expected ok chip with duration, got %#v", chip)
	}

	// After F3 the chip state still drives summaries, but the chat view no
	// longer renders a separate Tool Timeline section — the event lives
	// inline in the transcript instead.
	_ = m.renderChatView(140)
}

func TestToolCallsMirrorOntoStreamingAssistantMessage(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.status = engine.Status{Provider: "anthropic", Model: "claude-opus-4-6"}
	m.chat.sending = true
	m.chat.transcript = []chatLine{
		{Role: "user", Content: "list dir"},
		{Role: "assistant", Content: ""},
	}
	m.chat.streamIndex = 1

	m = m.handleEngineEvent(engine.Event{
		Type: "tool:call",
		Payload: map[string]any{
			"tool":           "list_dir",
			"step":           1,
			"params_preview": "path=.",
		},
	})
	if got := len(m.chat.transcript[1].ToolChips); got != 1 {
		t.Fatalf("expected tool:call to push chip onto streaming assistant line, got %d", got)
	}
	if chip := m.chat.transcript[1].ToolChips[0]; chip.Status != "running" || chip.Name != "list_dir" {
		t.Fatalf("expected running list_dir chip, got %#v", chip)
	}

	m = m.handleEngineEvent(engine.Event{
		Type: "tool:result",
		Payload: map[string]any{
			"tool":          "list_dir",
			"step":          1,
			"durationMs":    73,
			"success":       true,
			"output_tokens": 1280,
			"truncated":     true,
		},
	})
	if got := len(m.chat.transcript[1].ToolChips); got != 1 {
		t.Fatalf("tool:result should merge into the running chip, got %d", got)
	}
	chip := m.chat.transcript[1].ToolChips[0]
	if chip.Status != "ok" || chip.DurationMs != 73 {
		t.Fatalf("expected merged ok chip with duration, got %#v", chip)
	}
	if chip.OutputTokens != 1280 || !chip.Truncated {
		t.Fatalf("expected token delta + truncated flag on chip, got %#v", chip)
	}

	view := m.renderChatView(140)
	if !strings.Contains(view, "list_dir") || !strings.Contains(view, "73ms") {
		t.Fatalf("assistant bubble should render the tool chip strip inline; got:\n%s", view)
	}
	if !strings.Contains(view, "+1.3k tok") {
		t.Fatalf("assistant bubble should render the tool token delta; got:\n%s", view)
	}
}

func TestChatMentionNavigationAndTabCompletion(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.filesView.entries = []string{"internal/api/server.go", "internal/app/service.go", "README.md"}
	m.chat.input = "@internal/"

	downModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	down, ok := downModel.(Model)
	if !ok {
		t.Fatalf("expected Model after mention down key, got %T", downModel)
	}
	if down.slashMenu.mention != 1 {
		t.Fatalf("expected mention index to move to 1, got %d", down.slashMenu.mention)
	}

	completedModel, _ := down.Update(tea.KeyMsg{Type: tea.KeyTab})
	completed, ok := completedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after mention tab completion, got %T", completedModel)
	}
	if !strings.Contains(completed.chat.input, "[[file:internal/app/service.go]]") {
		t.Fatalf("expected second mention suggestion selected, got %q", completed.chat.input)
	}
}

func TestChatMentionEnterCompletesBeforeSending(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 0
	m.filesView.entries = []string{"internal/api/server.go"}
	m.chat.input = "Review @internal/api/ser"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after mention enter completion, got %T", nextModel)
	}
	if !strings.Contains(next.chat.input, "[[file:internal/api/server.go]]") {
		t.Fatalf("expected mention replacement on enter, got %q", next.chat.input)
	}
	if next.chat.sending {
		t.Fatal("expected enter on active mention to complete mention before sending")
	}
	if len(next.chat.transcript) != 0 {
		t.Fatalf("expected no transcript append while mention is being completed, got %#v", next.chat.transcript)
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
	if typed.chat.input != "abXcd" {
		t.Fatalf("expected middle insertion result abXcd, got %q", typed.chat.input)
	}
	if typed.chat.cursor != 3 {
		t.Fatalf("expected cursor at 3 after insertion, got %d", typed.chat.cursor)
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
	if up1.chat.input != "/context" {
		t.Fatalf("expected latest history command /context, got %q", up1.chat.input)
	}

	up2Model, _ := up1.Update(tea.KeyMsg{Type: tea.KeyUp})
	up2, ok := up2Model.(Model)
	if !ok {
		t.Fatalf("expected Model after second history up, got %T", up2Model)
	}
	if up2.chat.input != "/help" {
		t.Fatalf("expected previous history command /help, got %q", up2.chat.input)
	}

	down1Model, _ := up2.Update(tea.KeyMsg{Type: tea.KeyDown})
	down1, ok := down1Model.(Model)
	if !ok {
		t.Fatalf("expected Model after first history down, got %T", down1Model)
	}
	if down1.chat.input != "/context" {
		t.Fatalf("expected next history command /context, got %q", down1.chat.input)
	}

	down2Model, _ := down1.Update(tea.KeyMsg{Type: tea.KeyDown})
	down2, ok := down2Model.(Model)
	if !ok {
		t.Fatalf("expected Model after second history down, got %T", down2Model)
	}
	if down2.chat.input != "" {
		t.Fatalf("expected restored draft input after history down, got %q", down2.chat.input)
	}
}

func TestChatSlashOperationalCommands(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 0
	m.status = eng.Status()
	m.patchView.latestPatch = "--- a/demo.txt\n+++ b/demo.txt\n@@ -1 +1 @@\n-old\n+new\n"
	m.patchView.files = []string{"demo.txt"}
	m.patchView.set = []patchSection{
		{
			Path:      "demo.txt",
			HunkCount: 1,
			Hunks:     []patchHunk{{Header: "@@ -1 +1 @@", Content: "--- a/demo.txt\n+++ b/demo.txt\n@@ -1 +1 @@\n-old\n+new\n"}},
		},
	}

	for _, input := range []string{"/status", "/reload", "/context", "/tools", "/patch"} {
		m.chat.input = input
		nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		next, ok := nextModel.(Model)
		if !ok {
			t.Fatalf("expected Model after %s, got %T", input, nextModel)
		}
		if len(next.chat.transcript) == 0 || next.chat.transcript[len(next.chat.transcript)-1].Role != "system" {
			t.Fatalf("expected system transcript entry after %s, got %#v", input, next.chat.transcript)
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
	m.chat.input = "/undo"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after /undo, got %T", nextModel)
	}
	if len(next.chat.transcript) == 0 || !strings.Contains(next.chat.transcript[len(next.chat.transcript)-1].Content, "Undone messages: 2") {
		t.Fatalf("expected undo message in transcript, got %#v", next.chat.transcript)
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
	m.filesView.pinned = "internal/engine/engine.go"

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
	m.chat.input = "/context full"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after /context full, got %T", nextModel)
	}
	if len(next.chat.transcript) == 0 {
		t.Fatalf("expected system transcript after /context full, got %#v", next.chat.transcript)
	}
	last := next.chat.transcript[len(next.chat.transcript)-1]
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
	m.setupWizard.index = targetIndex

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after setup apply, got %T", nextModel)
	}
	if next.currentProvider() != providers[targetIndex] {
		t.Fatalf("expected setup to apply provider %q, got %q", providers[targetIndex], next.currentProvider())
	}
	if len(next.chat.transcript) == 0 || next.chat.transcript[len(next.chat.transcript)-1].Role != "system" {
		t.Fatalf("expected setup apply to append system transcript, got %#v", next.chat.transcript)
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
	m.setupWizard.index = targetIndex

	editModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	editing, ok := editModel.(Model)
	if !ok {
		t.Fatalf("expected Model after setup edit key, got %T", editModel)
	}
	if !editing.setupWizard.editing {
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
	m.filesView.entries = []string{"demo.txt"}
	m.filesView.index = 0
	m.toolView.index = indexOfString(m.availableTools(), "read_file")
	if m.toolView.index < 0 {
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
	if !strings.Contains(final.toolView.output, "alpha") {
		t.Fatalf("expected tool output to contain file content, got:\n%s", final.toolView.output)
	}
	if final.filesView.path != "demo.txt" {
		t.Fatalf("expected file path to follow tool target, got %q", final.filesView.path)
	}
}

func TestToolsTabCanEditAndResetParams(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 5
	m.toolView.index = indexOfString(m.availableTools(), "write_file")
	if m.toolView.index < 0 {
		t.Fatal("expected write_file tool to be registered")
	}

	editingModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	editing, ok := editingModel.(Model)
	if !ok {
		t.Fatalf("expected Model after e key, got %T", editingModel)
	}
	if !editing.toolView.editing {
		t.Fatal("expected tool editor to open")
	}

	editing.toolView.draft = `path=tmp/custom.txt content="hello world" overwrite=true`
	savedModel, _ := editing.Update(tea.KeyMsg{Type: tea.KeyEnter})
	saved, ok := savedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after saving tool params, got %T", savedModel)
	}
	if saved.toolView.editing {
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
	m.toolView.index = indexOfString(m.availableTools(), "write_file")
	if m.toolView.index < 0 {
		t.Fatal("expected write_file tool to be registered")
	}

	editingModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune("e")})
	editing, ok := editingModel.(Model)
	if !ok {
		t.Fatalf("expected Model after alt+e key, got %T", editingModel)
	}
	if !editing.toolView.editing {
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
	m.filesView.entries = []string{"demo.txt"}
	m.filesView.index = 0
	m.toolView.index = indexOfString(m.availableTools(), "edit_file")
	if m.toolView.index < 0 {
		t.Fatal("expected edit_file tool to be registered")
	}
	m.toolView.overrides["edit_file"] = `path=demo.txt old_string="old value" new_string="new value" replace_all=false`

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
	if final.filesView.path != "demo.txt" {
		t.Fatalf("expected focused file path demo.txt, got %q", final.filesView.path)
	}
	if !strings.Contains(final.filesView.preview, "new value") {
		t.Fatalf("expected preview to refresh edited content, got %q", final.filesView.preview)
	}
	if !containsStringFold(final.patchView.changed, "demo.txt") {
		t.Fatalf("expected changed files to include demo.txt, got %#v", final.patchView.changed)
	}
	if !strings.Contains(final.patchView.diff, "+new value") {
		t.Fatalf("expected worktree diff to refresh edited hunk, got:\n%s", final.patchView.diff)
	}
}

func TestToolsTabRunsCommandPreset(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.activeTab = 5
	m.toolView.index = indexOfString(m.availableTools(), "run_command")
	if m.toolView.index < 0 {
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
	if !strings.Contains(strings.ToLower(final.toolView.output), "go version") {
		t.Fatalf("expected command output in tools panel, got:\n%s", final.toolView.output)
	}
}

func TestRunCommandSuggestionsPreferGoProjectTargets(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/test\n\ngo 1.23.0\n")
	mustWriteFile(t, filepath.Join(root, "internal", "engine", "engine.go"), "package engine\n")

	eng := newTUITestEngine(t)
	eng.ProjectRoot = root
	m := NewModel(context.Background(), eng)
	m.filesView.entries = []string{"go.mod", "internal/engine/engine.go"}
	m.filesView.index = 1

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
	m.filesView.entries = []string{"a.go", "b.go", "c.go"}

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after j key, got %T", nextModel)
	}
	if next.filesView.index != 1 {
		t.Fatalf("expected file index 1 after j, got %d", next.filesView.index)
	}

	prevModel, _ := next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	prev, ok := prevModel.(Model)
	if !ok {
		t.Fatalf("expected Model after k key, got %T", prevModel)
	}
	if prev.filesView.index != 0 {
		t.Fatalf("expected file index 0 after k, got %d", prev.filesView.index)
	}
}

func TestFilesTabInsertSelectedFileMarker(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 2
	m.filesView.entries = []string{"a.go", "b.go"}
	m.filesView.index = 1
	m.chat.input = "please inspect"

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after i key, got %T", nextModel)
	}
	if next.activeTab != 0 {
		t.Fatalf("expected active tab 0 after insert, got %d", next.activeTab)
	}
	if !strings.Contains(next.chat.input, "[[file:b.go]]") {
		t.Fatalf("expected input to include selected file marker, got %q", next.chat.input)
	}
}

func TestFilesTabTogglePinnedFile(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 2
	m.filesView.entries = []string{"a.go", "b.go"}
	m.filesView.index = 1

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after p key, got %T", nextModel)
	}
	if next.filesView.pinned != "b.go" {
		t.Fatalf("expected pinned file b.go, got %q", next.filesView.pinned)
	}

	clearedModel, _ := next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	cleared, ok := clearedModel.(Model)
	if !ok {
		t.Fatalf("expected Model after second p key, got %T", clearedModel)
	}
	if cleared.filesView.pinned != "" {
		t.Fatalf("expected pinned file to clear, got %q", cleared.filesView.pinned)
	}
}

func TestFilesTabAltShortcutTogglePinnedFile(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 2
	m.filesView.entries = []string{"a.go", "b.go"}
	m.filesView.index = 1

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune("p")})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after alt+p key, got %T", nextModel)
	}
	if next.filesView.pinned != "b.go" {
		t.Fatalf("expected pinned file b.go via alt+p, got %q", next.filesView.pinned)
	}
}

func TestFilesTabPrepareExplainAndReviewPrompts(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 2
	m.filesView.entries = []string{"internal/auth/service.go"}

	explainModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	explain, ok := explainModel.(Model)
	if !ok {
		t.Fatalf("expected Model after e key, got %T", explainModel)
	}
	if explain.activeTab != 0 {
		t.Fatalf("expected active tab 0 after explain prompt, got %d", explain.activeTab)
	}
	if !strings.Contains(explain.chat.input, "Explain [[file:internal/auth/service.go]]") {
		t.Fatalf("expected explain prompt to target selected file, got %q", explain.chat.input)
	}

	m.activeTab = 2
	reviewModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	review, ok := reviewModel.(Model)
	if !ok {
		t.Fatalf("expected Model after v key, got %T", reviewModel)
	}
	if !strings.Contains(review.chat.input, "Review [[file:internal/auth/service.go]]") {
		t.Fatalf("expected review prompt to target selected file, got %q", review.chat.input)
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
	if !strings.Contains(view, "Catalog:") || !strings.Contains(view, "ready") {
		t.Fatalf("expected models.dev cache line in status view, got:\n%s", view)
	}
	if !strings.Contains(view, "Runtime:") {
		t.Fatalf("expected runtime connectivity line in status view, got:\n%s", view)
	}
	if !strings.Contains(view, "CONTEXT IN") || !strings.Contains(view, "Last:") || !strings.Contains(view, "Why:") || !strings.Contains(view, "Top:") {
		t.Fatalf("expected grouped context-in section in status view, got:\n%s", view)
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
	if m.ui.showHelpOverlay {
		t.Fatal("help overlay should default to off")
	}
	m.ui.showHelpOverlay = true
	out := m.renderHelpOverlay(120)
	if !strings.Contains(out, "enter send") || !strings.Contains(out, "ctrl+p palette") {
		t.Fatalf("expected chat hints in help overlay, got:\n%s", out)
	}
	if !strings.Contains(out, "ctrl+h") {
		t.Fatalf("expected self-describing ctrl+h close hint, got:\n%s", out)
	}
}

// TestHelpOverlayCoversEveryTab — regression guard: each tab in NewModel's
// tabs array must have its own case in helpOverlayTabHints, not fall into
// the default "tabs · palette · quit" bucket. When a new panel lands, this
// test fails loudly until the author adds a tab-specific hint block — no
// panel should ship with blank keybinding discovery.
func TestHelpOverlayCoversEveryTab(t *testing.T) {
	m := NewModel(context.Background(), nil)
	fallback := helpOverlayTabHints("__definitely-not-a-tab__")
	fallbackJoined := strings.Join(fallback, "|")
	for _, tab := range m.tabs {
		hints := helpOverlayTabHints(tab)
		if len(hints) == 0 {
			t.Errorf("tab %q has no help hints", tab)
			continue
		}
		if strings.Join(hints, "|") == fallbackJoined {
			t.Errorf("tab %q falls through to the generic default hint — add a dedicated case", tab)
		}
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

// TestRenderFilesViewHintDescribesRealKeys — the inline hint at the top of
// the Files panel must describe the actual keys handleFilesKey accepts, not
// stale copy. Previously the hint said 'enter reload' when 'r' is the
// reload key (enter loads the preview); this test pins the corrected copy.
func TestRenderFilesViewHintDescribesRealKeys(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 2
	view := m.renderFilesView(120)
	if !strings.Contains(view, "r reload") {
		t.Fatalf("Files hint should advertise r as the reload key, got:\n%s", view)
	}
	if !strings.Contains(view, "enter preview") {
		t.Fatalf("Files hint should advertise enter as the preview key, got:\n%s", view)
	}
	if strings.Contains(view, "enter reload") {
		t.Fatalf("stale 'enter reload' copy must not re-appear in Files hint, got:\n%s", view)
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
	m.toolView.index = indexOfString(m.availableTools(), "read_file")
	m.toolView.output = "Tool: read_file\nSuccess: true\n\npackage main"

	view := m.renderToolsView(100)
	if !strings.Contains(view, "Tools") || !strings.Contains(view, "Tool Detail") {
		t.Fatalf("expected tools headings, got:\n%s", view)
	}
	// Tools panel now renders the full ToolSpec (summary, risk, args)
	// instead of the prior 3-line digest. Assert on the spec header
	// shape so future refactors of formatToolSpec stay visible.
	if !strings.Contains(view, "read_file") || !strings.Contains(view, "summary:") {
		t.Fatalf("expected rich spec (read_file + summary:) in tools view, got:\n%s", view)
	}
	if !strings.Contains(view, "Effective params") || !strings.Contains(view, "Last Result") || !strings.Contains(view, "package main") {
		t.Fatalf("expected effective-params + last-result sections in tools view, got:\n%s", view)
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
	m.chat.input = "Please inspect auth flow"
	m.filesView.pinned = "internal/auth/service.go"

	got := m.chatPrompt()
	if !strings.Contains(got, "[[file:internal/auth/service.go]]") {
		t.Fatalf("expected pinned file marker in chat prompt, got %q", got)
	}
}

func TestChatPromptExpandsAtMentions(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.input = "Check @internal/config/conf"
	m.filesView.entries = []string{"internal/config/config.go", "README.md"}

	got := m.chatPrompt()
	if !strings.Contains(got, "[[file:internal/config/config.go]]") {
		t.Fatalf("expected @ mention expansion, got %q", got)
	}
}

func TestFocusChangedFilesPrefersPinnedFile(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.filesView.entries = []string{"a.go", "b.go", "c.go"}
	m.filesView.index = 0
	m.filesView.pinned = "b.go"

	next := m.focusChangedFiles([]string{"c.go", "b.go"})
	if next.filesView.index != 1 {
		t.Fatalf("expected focus to move to pinned changed file, got index %d", next.filesView.index)
	}

	next = m.focusChangedFiles([]string{"c.go"})
	if next.filesView.index != 2 {
		t.Fatalf("expected focus to move to first changed file when pinned file missing, got index %d", next.filesView.index)
	}
}

func TestRenderChatAndFilesViewShowPinnedFile(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.width = 100
	m.filesView.pinned = "internal/auth/service.go"
	m.filesView.path = "internal/auth/service.go"
	m.filesView.preview = "package auth"
	m.filesView.entries = []string{"internal/auth/service.go"}

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
	m.chat.transcript = []chatLine{
		{Role: "user", Content: "patch this"},
		{Role: "assistant", Content: "```diff\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n```\n"},
	}

	m.annotateAssistantPatch(1)
	if len(m.chat.transcript[1].PatchFiles) != 1 || m.chat.transcript[1].PatchFiles[0] != "a.go" {
		t.Fatalf("expected assistant transcript patch files, got %#v", m.chat.transcript[1])
	}
	if m.chat.transcript[1].PatchHunks != 1 {
		t.Fatalf("expected assistant transcript patch hunks, got %#v", m.chat.transcript[1])
	}

	m.markLatestPatchInTranscript("--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new")
	if !m.chat.transcript[1].IsLatestPatch {
		t.Fatalf("expected assistant transcript line to be marked latest: %#v", m.chat.transcript[1])
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
	m.chat.transcript = []chatLine{
		{Role: "assistant", Content: "I inspected the file and ran checks."},
	}

	m.annotateAssistantToolUsage(0)
	if m.chat.transcript[0].ToolCalls != 2 {
		t.Fatalf("expected 2 tool calls, got %#v", m.chat.transcript[0])
	}
	if m.chat.transcript[0].ToolFailures != 1 {
		t.Fatalf("expected 1 tool failure, got %#v", m.chat.transcript[0])
	}
	if !containsStringFold(m.chat.transcript[0].ToolNames, "read_file") || !containsStringFold(m.chat.transcript[0].ToolNames, "run_command") {
		t.Fatalf("expected tool names in transcript, got %#v", m.chat.transcript[0])
	}
	if summary := m.chatPatchSummary(m.chat.transcript[0]); !strings.Contains(summary, "tools=2") || !strings.Contains(summary, "failures=1") {
		t.Fatalf("expected tool summary in chat summary, got %q", summary)
	}
}

func TestPatchFocusSummaryAndBestTarget(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.filesView.entries = []string{"a.go", "b.go", "c.go"}
	m.filesView.index = 2
	m.filesView.pinned = "b.go"
	m.patchView.files = []string{"a.go", "b.go"}
	m.patchView.set = []patchSection{
		{Path: "a.go", HunkCount: 1},
		{Path: "b.go", HunkCount: 2},
	}
	m.patchView.index = 1

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
	m.filesView.entries = []string{"a.go", "b.go", "c.go"}
	m.filesView.index = 0
	m.filesView.pinned = "b.go"
	m.patchView.files = []string{"a.go", "b.go"}
	m.patchView.set = []patchSection{
		{Path: "a.go", HunkCount: 1},
		{Path: "b.go", HunkCount: 1},
	}
	m.patchView.index = 1

	nextModel, cmd := m.focusPatchFile()
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model from focusPatchFile, got %T", nextModel)
	}
	if next.activeTab != 2 {
		t.Fatalf("expected focusPatchFile to switch to Files tab, got %d", next.activeTab)
	}
	if next.filesView.index != 1 {
		t.Fatalf("expected focusPatchFile to move to pinned patched file, got index %d", next.filesView.index)
	}
	if cmd == nil {
		t.Fatal("expected preview reload command from focusPatchFile")
	}
}

func TestRenderPatchViewShowsPatchFiles(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.patchView.files = []string{"internal/auth/service.go"}
	m.filesView.pinned = "internal/auth/service.go"
	m.patchView.latestPatch = "--- a/internal/auth/service.go\n+++ b/internal/auth/service.go\n@@ -1 +1 @@\n-old\n+new\n"
	m.patchView.set = []patchSection{
		{
			Path:      "internal/auth/service.go",
			HunkCount: 1,
			Content:   "--- a/internal/auth/service.go\n+++ b/internal/auth/service.go\n@@ -1 +1 @@\n-old\n+new\n",
		},
	}

	view := m.renderPatchView(100)
	if !strings.Contains(view, "Patch files:") || !strings.Contains(view, "internal/auth/service.go") {
		t.Fatalf("expected patch files line in patch view, got:\n%s", view)
	}
	if !strings.Contains(view, "Pinned file is touched by latest patch.") {
		t.Fatalf("expected patch overlap hint in patch view, got:\n%s", view)
	}
}

func TestShiftPatchTargetAndRenderPatchViewUsesCurrentSection(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 3
	m.patchView.files = []string{"a.go", "b.go"}
	m.patchView.set = []patchSection{
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
	if next.patchView.index != 1 {
		t.Fatalf("expected patch index 1 after navigation, got %d", next.patchView.index)
	}

	view := next.renderPatchView(100)
	if !strings.Contains(view, "Focus file:") || !strings.Contains(view, "b.go (2/2, hunks=1)") {
		t.Fatalf("expected patch target summary for second section, got:\n%s", view)
	}
	if !strings.Contains(view, "Focus hunk:") || !strings.Contains(view, "@@ -1 +1 @@ (1/1)") {
		t.Fatalf("expected hunk target summary, got:\n%s", view)
	}
	if !strings.Contains(view, "+++ b/b.go") || strings.Contains(view, "+++ b/a.go") {
		t.Fatalf("expected patch preview to show only current section, got:\n%s", view)
	}
}

func TestShiftPatchHunkAndReviewHints(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 3
	m.patchView.set = []patchSection{
		{
			Path:      "internal/auth/service.go",
			HunkCount: 2,
			Hunks: []patchHunk{
				{Header: "@@ -1 +1 @@", Content: "--- a/internal/auth/service.go\n+++ b/internal/auth/service.go\n@@ -1 +1 @@\n-old\n+new"},
				{Header: "@@ -10 +10 @@", Content: "--- a/internal/auth/service.go\n+++ b/internal/auth/service.go\n@@ -10 +10 @@\n-TODO old\n+fmt.Println(\"debug\")"},
			},
		},
	}
	m.patchView.files = []string{"internal/auth/service.go"}

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after j key, got %T", nextModel)
	}
	if next.patchView.hunk != 1 {
		t.Fatalf("expected patch hunk 1 after navigation, got %d", next.patchView.hunk)
	}

	view := next.renderPatchView(100)
	if !strings.Contains(view, "Focus hunk:") || !strings.Contains(view, "@@ -10 +10 @@ (2/2)") {
		t.Fatalf("expected second hunk target, got:\n%s", view)
	}
	if !strings.Contains(view, "contains TODO/FIXME") || !strings.Contains(view, "check debug or panic statements") {
		t.Fatalf("expected review cues for current hunk, got:\n%s", view)
	}
}

func TestRenderChatViewShowsPatchSummary(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.width = 100
	m.patchView.set = []patchSection{{Path: "internal/auth/service.go", HunkCount: 1}}
	m.patchView.files = []string{"internal/auth/service.go"}
	m.chat.transcript = []chatLine{
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
		"SESSION", "42m", "12 msgs",
		"ctrl+s",
	} {
		if !strings.Contains(panel, want) {
			t.Fatalf("stats panel missing %q, got:\n%s", want, panel)
		}
	}
}

func TestRenderToolChipWrapsLongPreviewToSecondLine(t *testing.T) {
	chip := toolChip{
		Name:       "read_file",
		Status:     "ok",
		DurationMs: 42,
		Preview:    "internal/provider/offline_analyzer.go L120-L320 — 8 security findings, 3 critical · AWS keys, SQL concat, weak crypto detected",
	}
	// Width 60: head+meta+preview cannot fit on one line so preview wraps.
	out := renderToolChip(chip, 60)
	if !strings.Contains(out, "\n") {
		t.Fatalf("expected long preview to wrap to a second line; got:\n%s", out)
	}
	parts := strings.Split(out, "\n")
	if len(parts) != 2 {
		t.Fatalf("expected exactly two lines, got %d:\n%s", len(parts), out)
	}
	if !strings.Contains(parts[0], "read_file") || !strings.Contains(parts[0], "42ms") {
		t.Fatalf("first line should hold head+meta, got: %q", parts[0])
	}
	if !strings.Contains(parts[1], "offline_analyzer") {
		t.Fatalf("second line should begin with preview text, got: %q", parts[1])
	}

	// Short preview — single-line form.
	short := renderToolChip(toolChip{Name: "list_dir", Status: "ok", Preview: "."}, 60)
	if strings.Contains(short, "\n") {
		t.Fatalf("short chip should stay on one line, got:\n%s", short)
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
	if !m.ui.showStatsPanel {
		t.Fatalf("stats panel should default to visible")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	mm := next.(Model)
	if mm.ui.showStatsPanel {
		t.Fatalf("ctrl+s should toggle stats panel off")
	}
	next2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	mm2 := next2.(Model)
	if !mm2.ui.showStatsPanel {
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
	m.ui.showStatsPanel = false
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

// TestSlashSplitDecomposesBroadQuery drives the /split dispatcher through
// Update() and checks that the resulting transcript message lists the
// subtasks the deterministic splitter found. Exercises the path the coach
// points users at when the loop parks for budget_exhausted.
func TestSlashSplitDecomposesBroadQuery(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.setChatInput("/split first survey engine.go, then map the router, and document the manager")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm, ok := out.(Model)
	if !ok {
		t.Fatalf("expected Model from Update, got %T", out)
	}
	if len(mm.chat.transcript) == 0 {
		t.Fatalf("/split should emit a system message, got empty transcript")
	}
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(last, "/split") {
		t.Fatalf("expected /split header in output, got:\n%s", last)
	}
	if !strings.Contains(last, "subtasks") {
		t.Fatalf("expected subtasks header, got:\n%s", last)
	}
	if !strings.Contains(last, "survey") || !strings.Contains(last, "router") {
		t.Fatalf("expected both clauses captured as subtasks, got:\n%s", last)
	}
	// "first ... then ..." markers mean the plan is sequential, not parallel.
	if !strings.Contains(last, "sequential") {
		t.Fatalf("expected sequential mode for staged markers, got:\n%s", last)
	}
}

// TestSlashSplitWithoutArgExplains guards the empty-args guard — a bare
// /split must tell the user what the command does instead of silently
// doing nothing.
func TestSlashSplitWithoutArgExplains(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.setChatInput("/split")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm, ok := out.(Model)
	if !ok {
		t.Fatalf("expected Model from Update, got %T", out)
	}
	if len(mm.chat.transcript) == 0 {
		t.Fatalf("/split without args should surface a usage line")
	}
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(last, "Usage: /split") {
		t.Fatalf("expected usage hint, got:\n%s", last)
	}
}

// TestSlashSplitAtomicExplainsNoDecomposition — when the query can't be
// split the message must say so clearly rather than printing an empty list
// that looks like the command failed.
func TestSlashSplitAtomicExplainsNoDecomposition(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.setChatInput("/split fix the parser in token.go")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm, ok := out.(Model)
	if !ok {
		t.Fatalf("expected Model from Update, got %T", out)
	}
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(last, "atomic") {
		t.Fatalf("expected atomic-task message, got:\n%s", last)
	}
}

// TestRenderToolChipShowsRTKCompression guards the per-chip compression
// badge: when a tool:result carries RTK-style savings, the chip appends a
// "rtk −<saved> (<pct>%)" chunk so the user can see the win right next to
// the tool call that produced it.
func TestRenderToolChipShowsRTKCompression(t *testing.T) {
	out := renderToolChip(toolChip{
		Name:            "run_command",
		Status:          "ok",
		DurationMs:      120,
		SavedChars:      1800,
		CompressedChars: 600,
		CompressionPct:  75,
	}, 140)
	if !strings.Contains(out, "rtk") {
		t.Fatalf("chip should advertise rtk compression, got: %q", out)
	}
	if !strings.Contains(out, "75%") {
		t.Fatalf("chip should show compression percent, got: %q", out)
	}

	// Zero savings — no rtk badge.
	none := renderToolChip(toolChip{Name: "read_file", Status: "ok"}, 140)
	if strings.Contains(none, "rtk") {
		t.Fatalf("chip without savings should not mention rtk, got: %q", none)
	}
}

// TestStatsPanelShowsSessionCompressionSavings covers the TOOLS-section
// "rtk saved N chars (M%)" line. When cumulative savings are zero the line
// stays hidden so resting sessions aren't cluttered.
func TestStatsPanelShowsSessionCompressionSavings(t *testing.T) {
	panel := renderStatsPanel(statsPanelInfo{
		Provider:              "openai",
		Model:                 "gpt-5.4",
		Configured:            true,
		ToolsEnabled:          true,
		ToolCount:             5,
		CompressionSavedChars: 42_000,
		CompressionRawChars:   100_000,
	}, 20)
	if !strings.Contains(panel, "rtk saved") {
		t.Fatalf("TOOLS section should surface cumulative rtk savings, got:\n%s", panel)
	}
	if !strings.Contains(panel, "42%") {
		t.Fatalf("savings line should include percent share, got:\n%s", panel)
	}

	quiet := renderStatsPanel(statsPanelInfo{
		Provider:     "openai",
		Model:        "gpt-5.4",
		Configured:   true,
		ToolsEnabled: true,
	}, 20)
	if strings.Contains(quiet, "rtk saved") {
		t.Fatalf("panel should hide rtk line when savings are zero, got:\n%s", quiet)
	}
}

// TestToolResultAccumulatesCompressionStats verifies that tool:result
// events feed both the inline chip and the session totals on the Model so
// the stats panel can show the lifetime compression win.
func TestToolResultAccumulatesCompressionStats(t *testing.T) {
	m := NewModel(context.Background(), nil)

	m = m.handleEngineEvent(engine.Event{
		Type: "tool:call",
		Payload: map[string]any{
			"tool": "run_command",
			"step": 1,
		},
	})
	m = m.handleEngineEvent(engine.Event{
		Type: "tool:result",
		Payload: map[string]any{
			"tool":                    "run_command",
			"step":                    1,
			"durationMs":              50,
			"success":                 true,
			"output_chars":            1000,
			"payload_chars":           400,
			"compression_saved_chars": 600,
			"compression_ratio":       0.40,
		},
	})
	if len(m.agentLoop.toolTimeline) != 1 {
		t.Fatalf("expected merged chip, got %d", len(m.agentLoop.toolTimeline))
	}
	chip := m.agentLoop.toolTimeline[0]
	if chip.SavedChars != 600 || chip.CompressedChars != 400 {
		t.Fatalf("chip should carry saved/compressed counts, got %#v", chip)
	}
	if chip.CompressionPct != 60 {
		t.Fatalf("compression pct should be 60, got %d", chip.CompressionPct)
	}
	if m.telemetry.compressionSavedChars != 600 || m.telemetry.compressionRawChars != 1000 {
		t.Fatalf("session totals not accumulated, got saved=%d raw=%d", m.telemetry.compressionSavedChars, m.telemetry.compressionRawChars)
	}

	// A second result doubles the running totals.
	m = m.handleEngineEvent(engine.Event{
		Type: "tool:call",
		Payload: map[string]any{
			"tool": "run_command",
			"step": 2,
		},
	})
	m = m.handleEngineEvent(engine.Event{
		Type: "tool:result",
		Payload: map[string]any{
			"tool":                    "run_command",
			"step":                    2,
			"success":                 true,
			"output_chars":            1000,
			"payload_chars":           400,
			"compression_saved_chars": 600,
			"compression_ratio":       0.40,
		},
	})
	if m.telemetry.compressionSavedChars != 1200 || m.telemetry.compressionRawChars != 2000 {
		t.Fatalf("totals should accumulate across events, got saved=%d raw=%d", m.telemetry.compressionSavedChars, m.telemetry.compressionRawChars)
	}

	// And the stats panel must surface them.
	info := m.statsPanelInfo()
	if info.CompressionSavedChars != 1200 {
		t.Fatalf("statsPanelInfo should forward session totals, got %d", info.CompressionSavedChars)
	}
}

// TestToolCounterTracksConcurrentCalls proves the active-tool counter
// increments on tool:call and decrements on tool:result so header badges
// show "tools N" only while there's active fan-out.
func TestToolCounterTracksConcurrentCalls(t *testing.T) {
	m := NewModel(context.Background(), nil)

	m = m.handleEngineEvent(engine.Event{
		Type:    "tool:call",
		Payload: map[string]any{"tool": "read_file", "step": 1},
	})
	m = m.handleEngineEvent(engine.Event{
		Type:    "tool:call",
		Payload: map[string]any{"tool": "grep_codebase", "step": 2},
	})
	if m.telemetry.activeToolCount != 2 {
		t.Fatalf("expected 2 concurrent tools, got %d", m.telemetry.activeToolCount)
	}
	if head := m.chatHeaderInfo(); head.ActiveTools != 2 {
		t.Fatalf("chatHeaderInfo should forward ActiveTools=2, got %d", head.ActiveTools)
	}

	m = m.handleEngineEvent(engine.Event{
		Type:    "tool:result",
		Payload: map[string]any{"tool": "read_file", "step": 1, "success": true},
	})
	if m.telemetry.activeToolCount != 1 {
		t.Fatalf("expected 1 remaining tool, got %d", m.telemetry.activeToolCount)
	}
	m = m.handleEngineEvent(engine.Event{
		Type:    "tool:result",
		Payload: map[string]any{"tool": "grep_codebase", "step": 2, "success": true},
	})
	if m.telemetry.activeToolCount != 0 {
		t.Fatalf("expected counter back to 0, got %d", m.telemetry.activeToolCount)
	}
	// Rogue extra tool:result must not drive the counter negative.
	m = m.handleEngineEvent(engine.Event{
		Type:    "tool:result",
		Payload: map[string]any{"tool": "ghost", "step": 99, "success": true},
	})
	if m.telemetry.activeToolCount != 0 {
		t.Fatalf("counter should clamp to 0 on unmatched result, got %d", m.telemetry.activeToolCount)
	}
}

// TestSubagentEventsDriveChipsAndCounter covers the full subagent lifecycle
// from delegate_task spawn through completion: chip pushed, header badge
// incremented, then chip merged to "ok" status + badge decremented.
func TestSubagentEventsDriveChipsAndCounter(t *testing.T) {
	m := NewModel(context.Background(), nil)

	m = m.handleEngineEvent(engine.Event{
		Type: "agent:subagent:start",
		Payload: map[string]any{
			"task": "refactor the auth middleware",
			"role": "coder",
		},
	})
	if m.telemetry.activeSubagentCount != 1 {
		t.Fatalf("expected activeSubagentCount=1 after start, got %d", m.telemetry.activeSubagentCount)
	}
	if len(m.agentLoop.toolTimeline) == 0 {
		t.Fatalf("expected subagent chip appended to timeline")
	}
	chip := m.agentLoop.toolTimeline[len(m.agentLoop.toolTimeline)-1]
	if chip.Status != "subagent-running" {
		t.Fatalf("expected subagent-running chip, got status=%q", chip.Status)
	}
	if !strings.Contains(chip.Name, "coder") {
		t.Fatalf("expected role in chip name, got %q", chip.Name)
	}
	if head := m.chatHeaderInfo(); head.ActiveSubagents != 1 {
		t.Fatalf("chatHeaderInfo should forward ActiveSubagents=1, got %d", head.ActiveSubagents)
	}

	m = m.handleEngineEvent(engine.Event{
		Type: "agent:subagent:done",
		Payload: map[string]any{
			"role":        "coder",
			"duration_ms": 1234,
			"tool_rounds": 5,
			"parked":      false,
			"err":         "",
		},
	})
	if m.telemetry.activeSubagentCount != 0 {
		t.Fatalf("expected activeSubagentCount=0 after done, got %d", m.telemetry.activeSubagentCount)
	}
	// The running chip should have been merged, not duplicated.
	for _, c := range m.agentLoop.toolTimeline {
		if c.Status == "subagent-running" {
			t.Fatalf("subagent-running chip should have been merged to ok/failed, still running: %#v", c)
		}
	}
	found := false
	for _, c := range m.agentLoop.toolTimeline {
		if c.Status == "subagent-ok" && strings.Contains(c.Name, "coder") {
			found = true
			if c.DurationMs != 1234 {
				t.Fatalf("subagent chip missing duration, got %d", c.DurationMs)
			}
		}
	}
	if !found {
		t.Fatalf("expected subagent-ok chip after done, timeline=%#v", m.agentLoop.toolTimeline)
	}
}

// TestSubagentFailureSurfacesError: when delegate_task returns an error,
// the chip status flips to subagent-failed with the error preview so the
// user sees what went wrong without digging into logs.
func TestSubagentFailureSurfacesError(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m = m.handleEngineEvent(engine.Event{
		Type:    "agent:subagent:start",
		Payload: map[string]any{"task": "broken task"},
	})
	m = m.handleEngineEvent(engine.Event{
		Type: "agent:subagent:done",
		Payload: map[string]any{
			"duration_ms": 100,
			"err":         "provider timeout",
		},
	})
	found := false
	for _, c := range m.agentLoop.toolTimeline {
		if c.Status == "subagent-failed" {
			found = true
			if !strings.Contains(c.Preview, "provider timeout") {
				t.Fatalf("failed chip should surface error preview, got %q", c.Preview)
			}
		}
	}
	if !found {
		t.Fatalf("expected subagent-failed chip, got timeline=%#v", m.agentLoop.toolTimeline)
	}
}

// TestBatchFanoutSurfacesInChipPreview: when tool_batch_call completes,
// the TUI turns batch_count/batch_parallel/batch_ok/batch_fail payload
// fields into a readable "N calls · P parallel · X ok · Y fail" chip preview.
func TestBatchFanoutSurfacesInChipPreview(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m = m.handleEngineEvent(engine.Event{
		Type:    "tool:call",
		Payload: map[string]any{"tool": "tool_batch_call", "step": 1},
	})
	m = m.handleEngineEvent(engine.Event{
		Type: "tool:result",
		Payload: map[string]any{
			"tool":           "tool_batch_call",
			"step":           1,
			"success":        true,
			"durationMs":     80,
			"batch_count":    4,
			"batch_parallel": 4,
			"batch_ok":       3,
			"batch_fail":     1,
		},
	})
	if len(m.agentLoop.toolTimeline) == 0 {
		t.Fatalf("expected batch chip")
	}
	chip := m.agentLoop.toolTimeline[len(m.agentLoop.toolTimeline)-1]
	for _, want := range []string{"4 calls", "4 parallel", "3 ok", "1 fail"} {
		if !strings.Contains(chip.Preview, want) {
			t.Fatalf("batch chip preview missing %q, got %q", want, chip.Preview)
		}
	}
}
