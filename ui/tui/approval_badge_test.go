package tui

import (
	"strings"
	"testing"
)

func TestChatHeader_NoApprovalBadgeByDefault(t *testing.T) {
	header := renderChatHeader(chatHeaderInfo{
		Provider:     "anthropic",
		Model:        "claude-sonnet-4-6",
		Configured:   true,
		ToolsEnabled: true,
	}, 200)
	if strings.Contains(header, "APPROVAL") || strings.Contains(header, "gate on") {
		t.Fatalf("no gate badge expected when ApprovalGated=false, got:\n%s", header)
	}
}

func TestChatHeader_ShowsGateBadgeWhenApprovalGated(t *testing.T) {
	header := renderChatHeader(chatHeaderInfo{
		Provider:      "anthropic",
		Model:         "claude-sonnet-4-6",
		Configured:    true,
		ToolsEnabled:  true,
		ApprovalGated: true,
	}, 200)
	if !strings.Contains(header, "gate on") {
		t.Fatalf("expected gate-on badge when ApprovalGated=true, got:\n%s", header)
	}
}

func TestChatHeader_LoudBadgeWhileApprovalPending(t *testing.T) {
	header := renderChatHeader(chatHeaderInfo{
		Provider:        "anthropic",
		Model:           "claude-sonnet-4-6",
		Configured:      true,
		ToolsEnabled:    true,
		ApprovalGated:   true,
		ApprovalPending: true,
	}, 200)
	if !strings.Contains(header, "APPROVAL") {
		t.Fatalf("expected loud APPROVAL badge when ApprovalPending=true, got:\n%s", header)
	}
	// The soft "gate on" badge must yield to the loud one while a prompt is up.
	if strings.Contains(header, "gate on") {
		t.Fatalf("soft gate badge should hide behind loud APPROVAL badge, got:\n%s", header)
	}
}
