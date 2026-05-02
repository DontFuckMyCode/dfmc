package tui

import (
	"context"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/security"
	"github.com/dontfuckmycode/dfmc/pkg/types"
	toolruntime "github.com/dontfuckmycode/dfmc/internal/tools"
	tea "github.com/charmbracelet/bubbletea"
)

// --- Update reducer tests ---

func TestUpdate_WindowSizeMsg(t *testing.T) {
	m := NewModel(nil, nil)
	msg := tea.WindowSizeMsg{Width: 120, Height: 40}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.width != 120 || nm.height != 40 {
		t.Errorf("width/height: got %d/%d want 120/40", nm.width, nm.height)
	}
}

func TestUpdate_NonChatTab_MouseWheelIgnored(t *testing.T) {
	m := NewModel(nil, nil)
	m.activeTab = 1 // Status tab, not Chat
	msg := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.chat.scrollback != 0 {
		t.Error("non-Chat tab mouse wheel should not scroll")
	}
}

func TestUpdate_MouseWheelPressNotWheel(t *testing.T) {
	m := NewModel(nil, nil)
	m.activeTab = 0
	m.chat.scrollback = 5
	msg := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.chat.scrollback != 5 {
		t.Error("non-wheel button should not change scroll")
	}
}

func TestUpdate_eventSubscribedMsg_NilChannel(t *testing.T) {
	m := NewModel(nil, nil)
	msg := eventSubscribedMsg{ch: nil}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.eventSub != nil {
		t.Error("nil ch should not set eventSub")
	}
}

func TestUpdate_eventSubscribedMsg_ValidChannel(t *testing.T) {
	m := NewModel(nil, nil)
	ch := make(chan engine.Event, 1)
	msg := eventSubscribedMsg{ch: ch}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.eventSub == nil {
		t.Error("eventSub should be set")
	}
}

func TestUpdate_statusLoadedMsg(t *testing.T) {
	m := NewModel(nil, nil)
	status := engine.Status{Provider: "anthropic", Model: "sonnet"}
	msg := statusLoadedMsg{status: status}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.status.Provider != "anthropic" {
		t.Errorf("provider: got %s", nm.status.Provider)
	}
}

func TestUpdate_workspaceLoadedMsg_NoDiff(t *testing.T) {
	m := NewModel(nil, nil)
	msg := workspaceLoadedMsg{diff: "", changed: nil, err: nil}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice != "Working tree is clean." {
		t.Errorf("clean tree notice: got %q", nm.notice)
	}
}

func TestUpdate_workspaceLoadedMsg_WithChanged(t *testing.T) {
	m := NewModel(nil, nil)
	msg := workspaceLoadedMsg{diff: "diff content", changed: []string{"foo.go", "bar.go"}, err: nil}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice == "" {
		t.Error("expected notice with changed files")
	}
	if nm.patchView.diff == "" {
		t.Error("diff should be set")
	}
}

func TestUpdate_workspaceLoadedMsg_Error(t *testing.T) {
	m := NewModel(nil, nil)
	msg := workspaceLoadedMsg{err: &errReader{msg: "read error"}}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice == "" {
		t.Error("expected notice with error")
	}
}

func TestUpdate_latestPatchLoadedMsg_Empty(t *testing.T) {
	m := NewModel(nil, nil)
	msg := latestPatchLoadedMsg{patch: ""}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice != "No assistant patch found yet." {
		t.Errorf("empty patch notice: got %q", nm.notice)
	}
}

func TestUpdate_latestPatchLoadedMsg_WithContent(t *testing.T) {
	m := NewModel(nil, nil)
	msg := latestPatchLoadedMsg{patch: "--- a/foo.go\n+++ b/foo.go\n@@ -1 +1 @@"}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice != "Loaded latest assistant patch." {
		t.Errorf("patch notice: got %q", nm.notice)
	}
}

func TestUpdate_filesLoadedMsg_Empty(t *testing.T) {
	m := NewModel(nil, nil)
	msg := filesLoadedMsg{files: nil, err: nil}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice != "No project files found." {
		t.Errorf("empty files notice: got %q", nm.notice)
	}
}

func TestUpdate_filesLoadedMsg_WithFiles(t *testing.T) {
	m := NewModel(nil, nil)
	msg := filesLoadedMsg{files: []string{"main.go", "foo.go"}, err: nil}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if len(nm.filesView.entries) != 2 {
		t.Errorf("entries: got %d want 2", len(nm.filesView.entries))
	}
}

func TestUpdate_filePreviewLoadedMsg(t *testing.T) {
	m := NewModel(nil, nil)
	msg := filePreviewLoadedMsg{path: "main.go", content: "package main", size: 12, err: nil}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.filesView.path != "main.go" {
		t.Errorf("path: got %s", nm.filesView.path)
	}
	if nm.filesView.preview != "package main" {
		t.Errorf("preview: got %s", nm.filesView.preview)
	}
}

func TestUpdate_filePreviewLoadedMsg_Error(t *testing.T) {
	m := NewModel(nil, nil)
	msg := filePreviewLoadedMsg{err: &errReader{msg: "preview error"}}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice == "" {
		t.Error("expected notice with error")
	}
}

func TestUpdate_memoryLoadedMsg(t *testing.T) {
	m := NewModel(nil, nil)
	m.memory.scroll = 100 // beyond new entries
	msg := memoryLoadedMsg{entries: []types.MemoryEntry{{ID: "1", Tier: types.MemoryWorking, Value: "entry1"}}, tier: "working", err: nil}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.memory.loading != false {
		t.Error("loading should be false")
	}
	if nm.memory.scroll != 0 {
		t.Error("scroll should clamp to 0 when exceeding len")
	}
}

func TestUpdate_memoryLoadedMsg_Error(t *testing.T) {
	m := NewModel(nil, nil)
	msg := memoryLoadedMsg{err: &errReader{msg: "memory error"}}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.memory.err == "" {
		t.Error("expected err to be set")
	}
}

func TestUpdate_codemapLoadedMsg(t *testing.T) {
	m := NewModel(nil, nil)
	m.codemap.scroll = 100
	msg := codemapLoadedMsg{snap: codemapSnapshot{}, err: nil}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.codemap.loading != false {
		t.Error("loading should be false")
	}
	if nm.codemap.loaded != true {
		t.Error("loaded should be true")
	}
}

func TestUpdate_codemapLoadedMsg_Error(t *testing.T) {
	m := NewModel(nil, nil)
	msg := codemapLoadedMsg{err: &errReader{msg: "codemap error"}}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.codemap.err == "" {
		t.Error("expected err to be set")
	}
}

func TestUpdate_conversationsLoadedMsg(t *testing.T) {
	m := NewModel(nil, nil)
	m.conversations.scroll = 100
	msg := conversationsLoadedMsg{entries: []conversation.Summary{{ID: "c1"}}, err: nil}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.conversations.loading != false {
		t.Error("loading should be false")
	}
	if nm.conversations.scroll != 0 {
		t.Error("scroll should clamp when exceeding len")
	}
}

func TestUpdate_conversationPreviewMsg(t *testing.T) {
	m := NewModel(nil, nil)
	msg := conversationPreviewMsg{id: "c1", msgs: []types.Message{}, err: nil}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.conversations.previewID != "c1" {
		t.Errorf("previewID: got %s", nm.conversations.previewID)
	}
	if nm.notice == "" {
		t.Error("expected notice for preview")
	}
}

func TestUpdate_conversationPreviewMsg_Error(t *testing.T) {
	m := NewModel(nil, nil)
	msg := conversationPreviewMsg{err: &errReader{msg: "preview error"}}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice == "" {
		t.Error("expected notice with error")
	}
}

func TestUpdate_promptsLoadedMsg(t *testing.T) {
	m := NewModel(nil, nil)
	m.prompts.scroll = 100
	msg := promptsLoadedMsg{templates: []promptlib.Template{{ID: "test"}}, err: nil}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.prompts.loading != false {
		t.Error("loading should be false")
	}
}

func TestUpdate_securityLoadedMsg(t *testing.T) {
	m := NewModel(nil, nil)
	msg := securityLoadedMsg{report: &security.Report{}, err: nil}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.security.loading != false {
		t.Error("loading should be false")
	}
	if nm.security.loaded != true {
		t.Error("loaded should be true")
	}
}

func TestUpdate_syncModelsDevMsg_Error(t *testing.T) {
	m := NewModel(nil, nil)
	msg := syncModelsDevMsg{err: &errReader{msg: "sync error"}}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice == "" {
		t.Error("expected notice with error")
	}
	if nm.providers.syncing != false {
		t.Error("syncing should be false")
	}
}

func TestUpdate_patchApplyMsg_CheckOnly(t *testing.T) {
	m := NewModel(nil, nil)
	msg := patchApplyMsg{checkOnly: true, err: nil}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice != "Patch check passed." {
		t.Errorf("notice: got %q", nm.notice)
	}
}

func TestUpdate_patchApplyMsg_ApplyError(t *testing.T) {
	m := NewModel(nil, nil)
	msg := patchApplyMsg{checkOnly: false, err: &errReader{msg: "apply failed"}}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice == "" {
		t.Error("expected notice with error")
	}
}

func TestUpdate_conversationUndoMsg(t *testing.T) {
	m := NewModel(nil, nil)
	msg := conversationUndoMsg{removed: 3, err: nil}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice == "" {
		t.Error("expected notice after undo")
	}
}

func TestUpdate_conversationUndoMsg_Error(t *testing.T) {
	m := NewModel(nil, nil)
	msg := conversationUndoMsg{err: &errReader{msg: "undo error"}}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice == "" {
		t.Error("expected notice with error")
	}
}

func TestUpdate_toolRunMsg_Error(t *testing.T) {
	m := NewModel(nil, nil)
	m.chat.toolPending = true
	m.chat.toolName = "read_file"
	msg := toolRunMsg{name: "read_file", params: nil, result: toolruntime.Result{}, err: &errReader{msg: "tool failed"}}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice == "" {
		t.Error("expected notice with error")
	}
	if nm.chat.toolPending != false {
		t.Error("toolPending should be cleared on error")
	}
}

func TestUpdate_toolRunMsg_Success(t *testing.T) {
	m := NewModel(nil, nil)
	msg := toolRunMsg{name: "read_file", params: map[string]any{"path": "foo.go"}, result: toolruntime.Result{Success: true, DurationMs: 50}}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice == "" {
		t.Error("expected notice after success")
	}
}

func TestUpdate_toolRunMsg_MutationTool(t *testing.T) {
	m := NewModel(nil, nil)
	msg := toolRunMsg{name: "write_file", params: nil, result: toolruntime.Result{Success: true}, err: nil}
	_, _ = m.Update(msg)
	// write_file is a mutation tool - should trigger workspace reload
}

func TestUpdate_chatDeltaMsg_NoActiveStream(t *testing.T) {
	m := NewModel(nil, nil)
	m.chat.streamIndex = -1
	msg := chatDeltaMsg{delta: "hello"}
	m2, _ := m.Update(msg)
	// Should not panic - guard checks streamIndex bounds
	_ = m2.(Model)
}

func TestUpdate_chatDeltaMsg_WithActiveStream(t *testing.T) {
	m := NewModel(nil, nil)
	m.chat.streamIndex = 0
	m.chat.transcript = []chatLine{{Content: ""}}
	msg := chatDeltaMsg{delta: "hello"}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.chat.transcript[0].Content != "hello" {
		t.Errorf("delta applied: got %q", nm.chat.transcript[0].Content)
	}
}

func TestUpdate_spinnerTickMsg_NotSending(t *testing.T) {
	m := NewModel(nil, nil)
	m.chat.spinnerFrame = 0
	msg := spinnerTickMsg{}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.chat.spinnerTicking {
		t.Error("spinnerTicking should be false when not sending")
	}
}

func TestUpdate_spinnerTickMsg_Sending(t *testing.T) {
	m := NewModel(nil, nil)
	m.chat.sending = true
	m.chat.spinnerTicking = true
	msg := spinnerTickMsg{}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if !nm.chat.spinnerTicking {
		t.Error("spinnerTicking should stay true when sending")
	}
}

func TestUpdate_chatDoneMsg(t *testing.T) {
	m := NewModel(nil, nil)
	m.chat.sending = true
	m.chat.streamIndex = 0
	m.chat.streamStartedAt = time.Now().Add(-100 * time.Millisecond)
	m.chat.transcript = []chatLine{{Content: "partial"}}
	m2, _ := m.Update(chatDoneMsg{})
	nm := m2.(Model)
	if nm.chat.sending != false {
		t.Error("sending should be false after done")
	}
	if nm.chat.streamIndex != -1 {
		t.Error("streamIndex should be reset")
	}
}

func TestUpdate_chatErrMsg_UserCancelled(t *testing.T) {
	m := NewModel(nil, nil)
	m.chat.sending = true
	m.chat.userCancelledStream = true
	msg := chatErrMsg{err: context.Canceled}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice == "" {
		t.Error("expected notice for cancelled")
	}
	if nm.chat.sending != false {
		t.Error("sending should be false after cancel")
	}
}

func TestUpdate_chatErrMsg_RealError(t *testing.T) {
	m := NewModel(nil, nil)
	m.chat.sending = true
	msg := chatErrMsg{err: &errReader{msg: "connection reset"}}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.notice == "" {
		t.Error("expected notice with error")
	}
}

func TestUpdate_streamClosedMsg(t *testing.T) {
	m := NewModel(nil, nil)
	m.chat.sending = true
	ch := make(chan tea.Msg, 1)
	ch <- nil // populate so receive doesn't block
	m.chat.streamMessages = ch
	m2, _ := m.Update(streamClosedMsg{})
	nm := m2.(Model)
	if nm.chat.sending != false {
		t.Error("sending should be false")
	}
	if nm.chat.streamIndex != -1 {
		t.Error("streamIndex should be -1")
	}
}

func TestUpdate_heartbeatTickMsg(t *testing.T) {
	m := NewModel(nil, nil)
	msg := heartbeatTickMsg{}
	m2, _ := m.Update(msg)
	// Heartbeat just returns a cmd, model unchanged
	nm := m2.(Model)
	if nm.chat.spinnerFrame != m.chat.spinnerFrame {
		t.Error("heartbeat should not change spinnerFrame")
	}
}

func TestUpdate_gitInfoLoadedMsg(t *testing.T) {
	m := NewModel(nil, nil)
	msg := gitInfoLoadedMsg{info: gitWorkspaceInfo{Branch: "main", Inserted: 2, Deleted: 1}}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.gitInfo.Branch != "main" {
		t.Errorf("branch: got %s", nm.gitInfo.Branch)
	}
}

func TestUpdate_approvalRequestedMsg_SecondApproval(t *testing.T) {
	m := NewModel(nil, nil)
	// First approval - use real pendingApproval type
	resp := make(chan engine.ApprovalDecision, 1)
	m.pendingApproval = &pendingApproval{
		ID:   1,
		Req:  engine.ApprovalRequest{Tool: "write_file", Source: "agent"},
		resp: resp,
	}

	// Second approval - should be denied
	resp2 := make(chan engine.ApprovalDecision, 1)
	secondPending := &pendingApproval{
		ID:   2,
		Req:  engine.ApprovalRequest{Tool: "run_command", Source: "agent"},
		resp: resp2,
	}
	msg := approvalRequestedMsg{Pending: secondPending}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.pendingApproval != m.pendingApproval {
		t.Error("second approval should be denied, keeping first")
	}
}

func TestUpdate_approvalRequestedMsg_SwitchesToChat(t *testing.T) {
	m := NewModel(nil, nil)
	m.activeTab = 5 // some non-Chat tab
	resp := make(chan engine.ApprovalDecision, 1)
	msg := approvalRequestedMsg{Pending: &pendingApproval{
		ID:   1,
		Req:  engine.ApprovalRequest{Tool: "write_file", Source: "agent"},
		resp: resp,
	}}
	m2, _ := m.Update(msg)
	nm := m2.(Model)
	if nm.activeTab != 0 {
		t.Error("approval should switch to Chat tab (0)")
	}
}

// --- helper types ---

type errReader struct {
	msg string
}

func (e *errReader) Error() string { return e.msg }