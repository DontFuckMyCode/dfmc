package drive

import (
	"strings"
	"testing"
)

func TestSynthesizeVerificationTodo_Empty(t *testing.T) {
	if got := synthesizeVerificationTodo(nil); got != nil {
		t.Fatalf("expected nil for nil slice, got %+v", got)
	}
	if got := synthesizeVerificationTodo([]Todo{}); got != nil {
		t.Fatalf("expected nil for empty slice, got %+v", got)
	}
}

func TestSynthesizeVerificationTodo_ExplicitVerifySuppresses(t *testing.T) {
	todos := []Todo{
		{ID: "T1", Title: "Do work", WorkerClass: "coder", Verification: "required"},
		{ID: "V1", Title: "Verify everything", Kind: "verify", WorkerClass: "tester"},
	}
	if got := synthesizeVerificationTodo(todos); got != nil {
		t.Fatalf("expected nil when explicit verify todo exists, got %+v", got)
	}
}

func TestSynthesizeVerificationTodo_ExplicitTesterWithVerifyLanguage(t *testing.T) {
	todos := []Todo{
		{ID: "T1", Title: "Do work", WorkerClass: "coder", Verification: "required"},
		{ID: "T2", Title: "Run verification tests", WorkerClass: "tester", Verification: "required"},
	}
	if got := synthesizeVerificationTodo(todos); got != nil {
		t.Fatalf("expected nil when tester todo has verification language, got %+v", got)
	}
}

func TestSynthesizeVerificationTodo_NoTargets(t *testing.T) {
	// planner/researcher/synthesizer with verification="none" → no targets
	todos := []Todo{
		{ID: "T1", Title: "Plan", WorkerClass: "planner", Verification: "none"},
		{ID: "T2", Title: "Research", WorkerClass: "researcher", Verification: ""},
		{ID: "T3", Title: "Synth", WorkerClass: "synthesizer", Verification: "none"},
	}
	if got := synthesizeVerificationTodo(todos); got != nil {
		t.Fatalf("expected nil when no verification targets, got %+v", got)
	}
}

func TestSynthesizeVerificationTodo_BasicRequired(t *testing.T) {
	todos := []Todo{
		{ID: "T1", Title: "Patch auth", WorkerClass: "coder", Verification: "required", FileScope: []string{"auth.go", " auth_test.go "}},
		{ID: "T2", Title: "Update config", WorkerClass: "coder", Verification: "light", Labels: []string{"infra"}},
	}
	v := synthesizeVerificationTodo(todos)
	if v == nil {
		t.Fatal("expected non-nil verification todo")
	}
	if v.Kind != "verify" {
		t.Errorf("Kind = %q; want verify", v.Kind)
	}
	if !v.ReadOnly {
		t.Errorf("ReadOnly = false; want true for synthesized verifier")
	}
	if v.Origin != "supervisor" {
		t.Errorf("Origin = %q; want supervisor", v.Origin)
	}
	if v.WorkerClass != "tester" {
		t.Errorf("WorkerClass = %q; want tester", v.WorkerClass)
	}
	if v.ProviderTag != "test" {
		t.Errorf("ProviderTag = %q; want test", v.ProviderTag)
	}
	if v.Verification != "required" {
		t.Errorf("Verification = %q; want required", v.Verification)
	}
	if v.Title != "Verification pass" {
		t.Errorf("Title = %q; want Verification pass", v.Title)
	}
	if v.Status != TodoPending {
		t.Errorf("Status = %q; want pending", v.Status)
	}
	if v.Confidence != 1 {
		t.Errorf("Confidence = %v; want 1", v.Confidence)
	}
	// Dependencies should include both target IDs, sorted
	if len(v.DependsOn) != 2 || v.DependsOn[0] != "T1" || v.DependsOn[1] != "T2" {
		t.Errorf("DependsOn = %v; want [T1 T2]", v.DependsOn)
	}
	// File scope should be deduped, trimmed, sorted
	if len(v.FileScope) != 2 || v.FileScope[0] != "auth.go" || v.FileScope[1] != "auth_test.go" {
		t.Errorf("FileScope = %v; want [auth.go auth_test.go]", v.FileScope)
	}
	// Labels should include verification, supervisor + infra from target
	if !containsStr(v.Labels, "infra") {
		t.Errorf("Labels = %v; want infra included", v.Labels)
	}
	// Skills should include test and review
	if !containsStr(v.Skills, "test") || !containsStr(v.Skills, "review") {
		t.Errorf("Skills = %v; want test+review", v.Skills)
	}
	// Detail should mention both TODO IDs
	if !strings.Contains(v.Detail, "T1") || !strings.Contains(v.Detail, "T2") {
		t.Errorf("Detail missing TODO refs: %q", v.Detail)
	}
	// Detail should mention file scopes
	if !strings.Contains(v.Detail, "auth.go") {
		t.Errorf("Detail missing file scope: %q", v.Detail)
	}
}

func TestSynthesizeVerificationTodo_DeepVerification(t *testing.T) {
	todos := []Todo{
		{ID: "T1", Title: "Patch auth", WorkerClass: "security", Verification: "deep"},
	}
	v := synthesizeVerificationTodo(todos)
	if v == nil {
		t.Fatal("expected non-nil verification todo")
	}
	if v.Title != "Deep verification pass" {
		t.Errorf("Title = %q; want Deep verification pass", v.Title)
	}
	if v.Verification != "deep" {
		t.Errorf("Verification = %q; want deep", v.Verification)
	}
	if v.WorkerClass != "security" {
		t.Errorf("WorkerClass = %q; want security", v.WorkerClass)
	}
	if v.ProviderTag != "review" {
		t.Errorf("ProviderTag = %q; want review", v.ProviderTag)
	}
	if !containsStr(v.Skills, "audit") {
		t.Errorf("Skills = %v; want audit included for deep", v.Skills)
	}
	if !strings.Contains(v.Detail, "deeper regression") {
		t.Errorf("Detail should mention deeper regression: %q", v.Detail)
	}
}

func TestSynthesizeVerificationTodo_DeepFromWorkerClass(t *testing.T) {
	todos := []Todo{
		{ID: "T1", Title: "Security patch", WorkerClass: "security", Verification: "required"},
	}
	v := synthesizeVerificationTodo(todos)
	if v == nil {
		t.Fatal("expected non-nil verification todo")
	}
	if v.Title != "Deep verification pass" {
		t.Errorf("Title = %q; want Deep verification pass (security worker → deep)", v.Title)
	}
}

func TestSynthesizeVerificationTodo_AuditSkill(t *testing.T) {
	todos := []Todo{
		{ID: "T1", Title: "Patch", WorkerClass: "coder", Verification: "required", Skills: []string{"audit"}},
	}
	v := synthesizeVerificationTodo(todos)
	if v == nil {
		t.Fatal("expected non-nil verification todo")
	}
	if !containsStr(v.Skills, "audit") {
		t.Errorf("Skills = %v; want audit included", v.Skills)
	}
}

func TestNextVerificationID(t *testing.T) {
	cases := []struct {
		name  string
		todos []Todo
		want  string
	}{
		{name: "empty", todos: nil, want: "SV1"},
		{name: "no supervisor prefix", todos: []Todo{{ID: "T1"}, {ID: "V2"}}, want: "SV1"},
		{name: "single SV1", todos: []Todo{{ID: "SV1"}}, want: "SV2"},
		{name: "SV3 then SV1", todos: []Todo{{ID: "SV3"}, {ID: "SV1"}}, want: "SV4"},
		{name: "SV10", todos: []Todo{{ID: "SV10"}}, want: "SV11"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextVerificationID(tc.todos); got != tc.want {
				t.Errorf("nextVerificationID() = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestVerificationTargets(t *testing.T) {
	todos := []Todo{
		{ID: "T1", WorkerClass: "coder", Verification: "required"},
		{ID: "T2", WorkerClass: "planner", Verification: "required"},    // filtered: planner
		{ID: "T3", WorkerClass: "researcher", Verification: "light"},    // filtered: researcher
		{ID: "T4", WorkerClass: "documenter", Verification: "required"}, // filtered: documenter
		{ID: "T5", WorkerClass: "synthesizer", Verification: "deep"},    // filtered: synthesizer
		{ID: "V1", Kind: "verify", WorkerClass: "tester"},               // filtered: verify kind
		{ID: "T6", WorkerClass: "coder", Verification: ""},              // filtered: empty verification
		{ID: "T7", WorkerClass: "coder", Verification: "none"},          // filtered: none
		{ID: "T8", WorkerClass: "reviewer", Verification: "light"},
		{ID: "T9", WorkerClass: "security", Verification: "deep"},
	}
	targets := verificationTargets(todos)
	ids := make([]string, len(targets))
	for i, t2 := range targets {
		ids[i] = t2.ID
	}
	wantIDs := []string{"T1", "T8", "T9"}
	if len(ids) != len(wantIDs) {
		t.Fatalf("targets = %v; want %v", ids, wantIDs)
	}
	for i, id := range ids {
		if id != wantIDs[i] {
			t.Errorf("targets[%d] = %q; want %q", i, id, wantIDs[i])
		}
	}
}

func TestContainsVerificationLanguage(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"verify the changes", true},
		{"run verification", true},
		{"check for regression", true},
		{"run the test suite", true},
		{"build and lint", true},
		{"perform a sanity check", true},
		{"implement auth flow", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := containsVerificationLanguage(tc.input); got != tc.want {
			t.Errorf("containsVerificationLanguage(%q) = %v; want %v", tc.input, got, tc.want)
		}
	}
}

func TestHasExplicitVerificationTodo(t *testing.T) {
	cases := []struct {
		name  string
		todos []Todo
		want  bool
	}{
		{
			name:  "empty",
			todos: nil,
			want:  false,
		},
		{
			name:  "verify kind",
			todos: []Todo{{Kind: "verify"}},
			want:  true,
		},
		{
			name:  "tester with verify title",
			todos: []Todo{{WorkerClass: "tester", Title: "Verify the build"}},
			want:  true,
		},
		{
			name:  "security with lint detail",
			todos: []Todo{{WorkerClass: "security", Detail: "Run lint check"}},
			want:  true,
		},
		{
			name:  "review provider with test",
			todos: []Todo{{ProviderTag: "review", Title: "Test everything"}},
			want:  true,
		},
		{
			name:  "coder without verify language",
			todos: []Todo{{WorkerClass: "coder", Title: "Patch auth"}},
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasExplicitVerificationTodo(tc.todos); got != tc.want {
				t.Errorf("hasExplicitVerificationTodo() = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestAddUnique(t *testing.T) {
	s := []string{"foo", "bar"}
	addUnique(&s, "baz")
	if len(s) != 3 || s[2] != "baz" {
		t.Fatalf("addUnique baz: %v", s)
	}
	addUnique(&s, "FOO") // case-insensitive dedup
	if len(s) != 3 {
		t.Fatalf("addUnique FOO should not duplicate: %v", s)
	}
	addUnique(&s, "  ") // whitespace-only ignored
	if len(s) != 3 {
		t.Fatalf("addUnique whitespace should be ignored: %v", s)
	}
}

func TestSynthesizeVerificationTodo_IDIncrement(t *testing.T) {
	todos := []Todo{
		{ID: "SV5", Title: "Old supervisor verify", Kind: "work", WorkerClass: "coder", Verification: "required"},
		{ID: "V8", Title: "Planner generated id", Kind: "work", WorkerClass: "coder", Verification: "required"},
		{ID: "T1", Title: "New work", WorkerClass: "coder", Verification: "required"},
	}
	v := synthesizeVerificationTodo(todos)
	if v == nil {
		t.Fatal("expected non-nil")
	}
	// Only supervisor-owned SV-prefixed IDs are considered.
	if v.ID != "SV6" {
		t.Errorf("ID = %q; want SV6", v.ID)
	}
}

// helper
func containsStr(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}
