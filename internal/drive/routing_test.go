package drive

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestMatchRule_ProviderTag(t *testing.T) {
	rules := []config.RoutingRule{
		{ProviderTag: "code", Profile: "profile-code"},
		{ProviderTag: "review", Profile: "profile-review"},
	}
	f := RoutingField{ProviderTag: "code"}
	got := EvaluateRules(f, rules)
	if got == nil {
		t.Fatal("expected match, got nil")
	}
	if got.Profile != "profile-code" {
		t.Errorf("got %q, want %q", got.Profile, "profile-code")
	}
}

func TestMatchRule_Verification(t *testing.T) {
	rules := []config.RoutingRule{
		{Verification: "deep", Profile: "profile-deep"},
	}
	f := RoutingField{Verification: "deep"}
	got := EvaluateRules(f, rules)
	if got == nil {
		t.Fatal("expected match")
	}
	if got.Profile != "profile-deep" {
		t.Errorf("got %q", got.Profile)
	}
}

func TestMatchRule_MinConfidence(t *testing.T) {
	rules := []config.RoutingRule{
		{MinConfidence: 0.8, Profile: "profile-high"},
		{MinConfidence: 0.5, Profile: "profile-low"},
	}
	// Task confidence below both thresholds — should not match
	f := RoutingField{Confidence: 0.3}
	got := EvaluateRules(f, rules)
	if got != nil {
		t.Errorf("expected nil (no match), got %q", got.Profile)
	}

	// Task confidence in mid-range — matches the 0.5 rule, not 0.8
	f = RoutingField{Confidence: 0.6}
	got = EvaluateRules(f, rules)
	if got == nil {
		t.Fatal("expected match")
	}
	if got.Profile != "profile-low" {
		t.Errorf("got %q, want %q", got.Profile, "profile-low")
	}
}

func TestMatchRule_MultipleCriteria(t *testing.T) {
	rules := []config.RoutingRule{
		{ProviderTag: "code", Verification: "deep", Profile: "profile-code-deep"},
	}
	// ProviderTag matches, verification doesn't
	f := RoutingField{ProviderTag: "code", Verification: "light"}
	got := EvaluateRules(f, rules)
	if got != nil {
		t.Errorf("expected nil (verification mismatch), got %q", got.Profile)
	}
	// Both match
	f = RoutingField{ProviderTag: "code", Verification: "deep"}
	got = EvaluateRules(f, rules)
	if got == nil {
		t.Fatal("expected match")
	}
	if got.Profile != "profile-code-deep" {
		t.Errorf("got %q", got.Profile)
	}
}

func TestMatchRule_Wildcard(t *testing.T) {
	rules := []config.RoutingRule{
		{Profile: "profile-default"},
	}
	f := RoutingField{ProviderTag: "code", Verification: "deep"}
	got := EvaluateRules(f, rules)
	if got == nil {
		t.Fatal("expected match (empty fields are wildcard)")
	}
	if got.Profile != "profile-default" {
		t.Errorf("got %q", got.Profile)
	}
}

func TestMatchRule_PriorityOrder(t *testing.T) {
	rules := []config.RoutingRule{
		{ProviderTag: "code", Priority: 0, Profile: "profile-low-priority"},
		{ProviderTag: "code", Priority: 10, Profile: "profile-high-priority"},
	}
	f := RoutingField{ProviderTag: "code"}
	got := EvaluateRules(f, rules)
	if got == nil {
		t.Fatal("expected match")
	}
	if got.Profile != "profile-high-priority" {
		t.Errorf("got %q, want high-priority", got.Profile)
	}
}

func TestMatchRule_FirstWins(t *testing.T) {
	// Both match, but the one with higher priority comes first after sort
	rules := []config.RoutingRule{
		{Priority: 5, ProviderTag: "code", Profile: "profile-a"},
		{Priority: 5, ProviderTag: "code", Profile: "profile-b"},
	}
	f := RoutingField{ProviderTag: "code"}
	got := EvaluateRules(f, rules)
	if got == nil {
		t.Fatal("expected match")
	}
	// With equal priority, stable sort preserves original order
	// The first matching rule in original order should win
	if got.Profile != "profile-a" {
		t.Errorf("got %q, want profile-a (first match)", got.Profile)
	}
}

func TestMatchRule_Role(t *testing.T) {
	rules := []config.RoutingRule{
		{Role: "security_auditor", Profile: "profile-audit"},
	}
	f := RoutingField{Role: "security_auditor"}
	got := EvaluateRules(f, rules)
	if got == nil {
		t.Fatal("expected match")
	}
	if got.Profile != "profile-audit" {
		t.Errorf("got %q", got.Profile)
	}
}

func TestEvaluateRules_EmptyRules(t *testing.T) {
	f := RoutingField{ProviderTag: "code"}
	got := EvaluateRules(f, nil)
	if got != nil {
		t.Errorf("expected nil for nil rules, got %v", got)
	}
	got = EvaluateRules(f, []config.RoutingRule{})
	if got != nil {
		t.Errorf("expected nil for empty rules, got %v", got)
	}
}

func TestRuleProfile(t *testing.T) {
	if RuleProfile(nil) != "" {
		t.Error("RuleProfile(nil) should return empty string")
	}
	rule := &config.RoutingRule{Profile: "  my-profile  "}
	if got := RuleProfile(rule); got != "my-profile" {
		t.Errorf("got %q", got)
	}
}

func TestRuleModel(t *testing.T) {
	if RuleModel(nil) != "" {
		t.Error("RuleModel(nil) should return empty string")
	}
	rule := &config.RoutingRule{Profile: "p", Model: "  opus  "}
	if got := RuleModel(rule); got != "opus" {
		t.Errorf("got %q", got)
	}
}

func TestTodoToRoutingField(t *testing.T) {
	todo := Todo{
		ProviderTag:  "CODE",
		WorkerClass:  "coder",
		Verification: "deep",
		Confidence:   0.9,
		FileScope:    []string{"internal/foo.go", "pkg/bar.go"},
	}
	f := TodoToRoutingField(todo)
	if !strings.EqualFold(f.ProviderTag, "CODE") {
		t.Errorf("ProviderTag: got %q", f.ProviderTag)
	}
	if !strings.EqualFold(f.WorkerClass, "coder") {
		t.Errorf("WorkerClass: got %q", f.WorkerClass)
	}
	if !strings.EqualFold(f.Verification, "deep") {
		t.Errorf("Verification: got %q", f.Verification)
	}
	if f.Confidence != 0.9 {
		t.Errorf("Confidence: got %v", f.Confidence)
	}
	if len(f.FileScope) != 2 {
		t.Errorf("FileScope: got %v", f.FileScope)
	}
}

func TestMatchRule_FileScopeGlob(t *testing.T) {
	// Rules are evaluated in priority order (highest first); first match wins.
	// Since both rules have default Priority=0, original input order is preserved (stable sort).
	// "*.go" matches "main.go" but also "internal/auth/login.go" because * matches
	// path separators on Windows. For specific-path matching, use anchored patterns.
	rules := []config.RoutingRule{
		{FileScope: []string{"internal/**"}, Profile: "profile-internal"},
		{FileScope: []string{"*.go"}, Profile: "profile-go"},
	}
	// Matches internal/**
	f := RoutingField{FileScope: []string{"internal/auth/login.go"}}
	got := EvaluateRules(f, rules)
	if got == nil {
		t.Fatal("expected match")
	}
	if got.Profile != "profile-internal" {
		t.Errorf("got %q, want %q", got.Profile, "profile-internal")
	}

	// Does NOT match internal/** (not a sub-path match), but *.go matches the bare filename
	f2 := RoutingField{FileScope: []string{"main.go"}}
	got2 := EvaluateRules(f2, rules)
	if got2 == nil {
		t.Fatal("expected match")
	}
	// profile-go is second in input order; profile-internal doesn't match main.go,
	// so the loop falls through to *.go
	if got2.Profile != "profile-go" {
		t.Errorf("got %q, want %q", got2.Profile, "profile-go")
	}
}