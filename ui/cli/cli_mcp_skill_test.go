// Tests for the MCP-side skill bridge. Cover (a) the static surface
// (descriptors well-formed, names stable, schemas validate) and (b)
// dispatch contracts (missing args are tool-level errors not transport
// errors, list returns the builtin catalog, validate accepts both
// inline content and on-disk paths). End-to-end "actually run a skill
// against a real LLM" is not exercised here — that lives in the
// engine.Ask path's tests. The point of this layer is the wire shape
// an IDE host will see.

package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillMCPHandlerExposesExpectedTools(t *testing.T) {
	h := &skillMCPHandler{eng: newCLITestEngine(t)}
	tools := h.Tools()
	want := []string{
		"dfmc_skill_list",
		"dfmc_skill_show",
		"dfmc_skill_validate",
		"dfmc_skill_run",
		"dfmc_skill_explain",
	}
	if len(tools) != len(want) {
		t.Fatalf("expected %d tools, got %d", len(want), len(tools))
	}
	for i, tool := range tools {
		if tool.Name != want[i] {
			t.Errorf("tool[%d]: want %q got %q", i, want[i], tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %q has nil input schema", tool.Name)
		}
		if got := tool.InputSchema["type"]; got != "object" {
			t.Errorf("tool %q schema.type = %v, want \"object\"", tool.Name, got)
		}
	}
}

func TestSkillMCPHandlerHandlesPrefix(t *testing.T) {
	h := &skillMCPHandler{}
	if !h.Handles("dfmc_skill_list") {
		t.Error("Handles must accept dfmc_skill_list")
	}
	if !h.Handles("dfmc_skill_run") {
		t.Error("Handles must accept dfmc_skill_run")
	}
	if h.Handles("read_file") {
		t.Error("Handles must NOT claim regular tools")
	}
	if h.Handles("skill_list") {
		t.Error("Handles must require the dfmc_ prefix to avoid collisions")
	}
	if h.Handles("dfmc_drive_start") {
		t.Error("Handles must NOT claim other dfmc_* prefixes")
	}
	if h.Handles("") {
		t.Error("Handles must not match empty name")
	}
}

func TestSkillMCPCallListReturnsBuiltins(t *testing.T) {
	h := &skillMCPHandler{eng: newCLITestEngine(t)}
	res, err := h.Call(context.Background(), "dfmc_skill_list", []byte(`{}`))
	if err != nil {
		t.Fatalf("Call returned transport error %v", err)
	}
	if res.IsError {
		t.Fatalf("list should not error, got: %s", res.Content[0].Text)
	}
	var payload struct {
		Skills []skillSummary `json:"skills"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode result: %v\nbody: %s", err, res.Content[0].Text)
	}
	if len(payload.Skills) == 0 {
		t.Fatal("expected at least the builtin catalog, got 0 skills")
	}
	// Check at least one builtin is there with auto-activation.
	foundAuto := false
	for _, s := range payload.Skills {
		if s.Builtin && s.AutoActivate {
			foundAuto = true
		}
	}
	if !foundAuto {
		t.Error("expected at least one builtin skill to advertise auto_activate=true")
	}
}

func TestSkillMCPCallShowRejectsMissingName(t *testing.T) {
	h := &skillMCPHandler{eng: newCLITestEngine(t)}
	res, err := h.Call(context.Background(), "dfmc_skill_show", []byte(`{}`))
	if err != nil {
		t.Fatalf("Call returned transport error %v", err)
	}
	if !res.IsError {
		t.Fatal("missing name must surface as IsError:true")
	}
	if !strings.Contains(res.Content[0].Text, "name is required") {
		t.Errorf("error text must mention 'name is required'; got %q", res.Content[0].Text)
	}
}

func TestSkillMCPCallShowReturnsBuiltin(t *testing.T) {
	h := &skillMCPHandler{eng: newCLITestEngine(t)}
	res, err := h.Call(context.Background(), "dfmc_skill_show", []byte(`{"name":"audit"}`))
	if err != nil {
		t.Fatalf("Call returned transport error %v", err)
	}
	if res.IsError {
		t.Fatalf("show should not error for builtin 'audit', got: %s", res.Content[0].Text)
	}
	var payload struct {
		Skill skillSummary `json:"skill"`
		Body  string       `json:"body"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Skill.Name != "audit" {
		t.Errorf("expected name 'audit', got %q", payload.Skill.Name)
	}
	if !payload.Skill.Builtin {
		t.Error("audit should be marked builtin")
	}
	if payload.Body == "" {
		t.Error("expected non-empty body for audit")
	}
	if !payload.Skill.AutoActivate {
		t.Error("audit ships with triggers — should have auto_activate=true")
	}
}

func TestSkillMCPCallShowMissingSkill(t *testing.T) {
	h := &skillMCPHandler{eng: newCLITestEngine(t)}
	res, _ := h.Call(context.Background(), "dfmc_skill_show", []byte(`{"name":"this-skill-does-not-exist"}`))
	if !res.IsError {
		t.Fatal("nonexistent skill must surface as IsError:true")
	}
	if !strings.Contains(res.Content[0].Text, "skill not found") {
		t.Errorf("error must mention 'skill not found'; got %q", res.Content[0].Text)
	}
}

func TestSkillMCPCallValidateInline(t *testing.T) {
	h := &skillMCPHandler{eng: newCLITestEngine(t)}
	body := `---
name: ok-skill
description: A clean skill
---
# Body
content`
	args, _ := json.Marshal(map[string]any{"content": body})
	res, err := h.Call(context.Background(), "dfmc_skill_validate", args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.IsError {
		t.Fatalf("validate should not error, got: %s", res.Content[0].Text)
	}
	var payload struct {
		Ok          bool             `json:"ok"`
		Diagnostics []map[string]any `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !payload.Ok {
		t.Errorf("expected ok=true for clean skill, got diagnostics=%v", payload.Diagnostics)
	}
}

func TestSkillMCPCallValidateBrokenInline(t *testing.T) {
	h := &skillMCPHandler{eng: newCLITestEngine(t)}
	body := `---
name: bad
triggers:
  - "[unterminated"
---
# Body
content`
	args, _ := json.Marshal(map[string]any{"content": body})
	res, err := h.Call(context.Background(), "dfmc_skill_validate", args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.IsError {
		t.Fatalf("validate of broken skill must succeed at transport level, got tool-level error: %s", res.Content[0].Text)
	}
	var payload struct {
		Ok          bool             `json:"ok"`
		Diagnostics []map[string]any `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Ok {
		t.Error("expected ok=false for broken trigger regex")
	}
	if len(payload.Diagnostics) == 0 {
		t.Error("expected at least one diagnostic for broken regex")
	}
}

func TestSkillMCPCallValidateRejectsEmpty(t *testing.T) {
	h := &skillMCPHandler{eng: newCLITestEngine(t)}
	res, _ := h.Call(context.Background(), "dfmc_skill_validate", []byte(`{}`))
	if !res.IsError {
		t.Fatal("validate with no content/path must surface IsError:true")
	}
	if !strings.Contains(res.Content[0].Text, "content") || !strings.Contains(res.Content[0].Text, "path") {
		t.Errorf("error must hint at both 'content' and 'path'; got %q", res.Content[0].Text)
	}
}

func TestSkillMCPCallValidateOnDiskPath(t *testing.T) {
	tmp := t.TempDir()
	// Drop a SKILL.md on disk and validate it via path.
	skillPath := filepath.Join(tmp, "custom", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `---
name: custom
description: clean
---
# Body
text`
	if err := os.WriteFile(skillPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h := &skillMCPHandler{eng: newCLITestEngine(t)}
	args, _ := json.Marshal(map[string]any{"path": skillPath})
	res, err := h.Call(context.Background(), "dfmc_skill_validate", args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.IsError {
		t.Fatalf("validate of on-disk file must not error: %s", res.Content[0].Text)
	}
}

func TestSkillMCPCallRunRejectsMissingName(t *testing.T) {
	h := &skillMCPHandler{eng: newCLITestEngine(t)}
	res, _ := h.Call(context.Background(), "dfmc_skill_run", []byte(`{}`))
	if !res.IsError {
		t.Fatal("missing name must surface IsError:true")
	}
	if !strings.Contains(res.Content[0].Text, "name is required") {
		t.Errorf("error must mention 'name is required'; got %q", res.Content[0].Text)
	}
}

func TestSkillMCPCallRunMissingSkill(t *testing.T) {
	h := &skillMCPHandler{eng: newCLITestEngine(t)}
	res, _ := h.Call(context.Background(), "dfmc_skill_run", []byte(`{"name":"ghost-skill"}`))
	if !res.IsError {
		t.Fatal("missing skill must surface IsError:true")
	}
	if !strings.Contains(res.Content[0].Text, "skill not found") {
		t.Errorf("error must mention 'skill not found'; got %q", res.Content[0].Text)
	}
}

func TestSkillMCPCallExplain(t *testing.T) {
	h := &skillMCPHandler{eng: newCLITestEngine(t)}
	args, _ := json.Marshal(map[string]any{"query": "find security vulnerabilities"})
	res, err := h.Call(context.Background(), "dfmc_skill_explain", args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.IsError {
		t.Fatalf("explain should not error, got: %s", res.Content[0].Text)
	}
	var payload struct {
		Active []struct {
			Name           string  `json:"name"`
			Origin         string  `json:"origin"`
			MatchedPattern string  `json:"matched_pattern"`
			Weight         float64 `json:"weight"`
		} `json:"active"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Active) == 0 {
		t.Fatal("expected at least one active skill for security query")
	}
	if payload.Active[0].Name != "audit" {
		t.Errorf("expected first active to be 'audit', got %q", payload.Active[0].Name)
	}
	if payload.Active[0].Origin != "trigger" {
		t.Errorf("expected origin 'trigger', got %q", payload.Active[0].Origin)
	}
}

func TestSkillMCPCallExplainRejectsEmptyQuery(t *testing.T) {
	h := &skillMCPHandler{eng: newCLITestEngine(t)}
	res, _ := h.Call(context.Background(), "dfmc_skill_explain", []byte(`{}`))
	if !res.IsError {
		t.Fatal("missing query must surface IsError:true")
	}
	if !strings.Contains(res.Content[0].Text, "query is required") {
		t.Errorf("error must mention 'query is required'; got %q", res.Content[0].Text)
	}
}

func TestSkillMCPCallUnknownTool(t *testing.T) {
	h := &skillMCPHandler{eng: newCLITestEngine(t)}
	_, err := h.Call(context.Background(), "dfmc_skill_unknown", []byte(`{}`))
	if err == nil {
		t.Fatal("expected transport error for unknown tool name")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("error should say 'unknown tool', got %v", err)
	}
}
