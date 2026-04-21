package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestDetectOfflineTaskFromSystemStamp(t *testing.T) {
	cases := map[string]string{
		"You are DFMC.\nTask: review\nLanguage: go":     "review",
		"You are DFMC.\nTask: security · Language: go":  "security",
		"Task: explain this":                            "explain",
		"something unrelated":                           "general",
		"please run a /security audit on internal/auth": "security",
		"/review the tui panel":                         "review",
		"can you explain how the agent loop works":      "explain",
	}
	for in, want := range cases {
		if got := detectOfflineTask(in, ""); got != want {
			t.Fatalf("detectOfflineTask(%q) = %q, want %q", in, got, want)
		}
	}
}

// Regression for the report's "fragile task detection" finding: slash
// commands embedded inside paths, comments, or quoted strings must NOT
// promote the task. Anchoring the regex at line-start (after optional
// whitespace) is what enforces this — the previous strings.Contains
// check happily matched `/explain` inside a docstring or `/plan`
// inside `/plans/`.
func TestDetectOfflineTask_IgnoresSlashInsidePathsAndComments(t *testing.T) {
	cases := map[string]string{
		// Path-like contexts.
		"please look at tests/plans/golden_test.go":   "general",
		"open the file at internal/security/audit.go": "general",
		"refactor /home/user/code/project/main.go":    "general",
		// Slash command embedded in a sentence (not at line start).
		"the comment said // /review but it's stale":     "general",
		"a docstring with /* /explain markers */ inline": "general",
		// Quoted string holding a slash command.
		"the message body was \"please /debug it\"": "general",
	}
	for in, want := range cases {
		if got := detectOfflineTask(in, ""); got != want {
			t.Fatalf("detectOfflineTask(%q) = %q, want %q (unanchored slash should not trigger)", in, got, want)
		}
	}
}

// Slash command at line-start (with optional indent) MUST trigger,
// since that's the canonical user-typed shape.
func TestDetectOfflineTask_AnchoredSlashTriggers(t *testing.T) {
	cases := map[string]string{
		"/review main.go":             "review",
		"  /security audit auth/":     "security",
		"/refactor the engine struct": "refactor",
		"/debug the panic":            "debug",
		"/test the patch flow":        "test",
		"/plan the migration":         "planning",
		"line one\n/explain main.go":  "explain",
	}
	for in, want := range cases {
		if got := detectOfflineTask(in, ""); got != want {
			t.Fatalf("detectOfflineTask(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOfflineProviderSecurityHeuristics(t *testing.T) {
	sample := `package main

import "fmt"

const apiKey = "AKIAABCDEFGHIJKLMNOP"

func login(user string) {
	q := "SELECT * FROM users WHERE name = '" + user + "'"
	fmt.Println(q)
}
`
	p := NewOfflineProvider()
	resp, err := p.Complete(context.Background(), CompletionRequest{
		System:   "You are DFMC.\nTask: security",
		Messages: []Message{{Role: "user", Content: "audit this file"}},
		Context: []types.ContextChunk{{
			Path: "main.go", Language: "go", LineStart: 1, LineEnd: 11, Content: sample,
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	for _, want := range []string{"CRITICAL", "AWS access key", "main.go:5", "SQL string concatenation", "main.go:8"} {
		if !strings.Contains(resp.Text, want) {
			t.Fatalf("offline security report missing %q; got:\n%s", want, resp.Text)
		}
	}
}

func TestOfflineProviderReviewFlagsGoIssues(t *testing.T) {
	sample := `package foo

func Bar() {
	_, _ = someCall()      // error discarded
	panic("boom")          // bare panic
	// TODO: remove this hack
}

func someCall() (int, error) { return 0, nil }
`
	p := NewOfflineProvider()
	resp, err := p.Complete(context.Background(), CompletionRequest{
		System:   "Task: review",
		Messages: []Message{{Role: "user", Content: "/review"}},
		Context: []types.ContextChunk{{
			Path: "foo.go", Language: "go", LineStart: 1, LineEnd: 9, Content: sample,
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	for _, want := range []string{"error return discarded", "panic()", "TODO marker"} {
		if !strings.Contains(resp.Text, want) {
			t.Fatalf("offline review missing %q; got:\n%s", want, resp.Text)
		}
	}
}

func TestOfflineProviderGeneralFallbackStillHelpful(t *testing.T) {
	p := NewOfflineProvider()
	resp, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: "user", Content: "what does this do?"}},
		Context: []types.ContextChunk{{
			Path: "util.go", Language: "go", LineStart: 1, LineEnd: 3,
			Content: "package util\n\nfunc Hello() string { return \"hi\" }\n",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !strings.Contains(resp.Text, "util.go") {
		t.Fatalf("general report should cite the file; got:\n%s", resp.Text)
	}
	if strings.Contains(resp.Text, "Offline mode is active. I analyzed") {
		t.Fatalf("old stub text should be gone; got:\n%s", resp.Text)
	}
}
