package bridge

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/drive"
)

func TestNormalizeDriveExecution_SecurityReviewDefaultsReadOnlyAudit(t *testing.T) {
	req := NormalizeDriveExecution(drive.ExecuteTodoRequest{
		Title:  "Audit auth boundary",
		Detail: "Review token validation, authz checks, and secret handling.",
	})
	if req.Role != "security_auditor" {
		t.Fatalf("expected security_auditor role, got %+v", req)
	}
	if req.Verification != "deep" {
		t.Fatalf("expected deep verification, got %+v", req)
	}
	if len(req.Skills) == 0 || req.Skills[0] != "audit" {
		t.Fatalf("expected audit skill, got %+v", req)
	}
	for _, banned := range []string{"edit_file", "apply_patch", "write_file"} {
		for _, allowed := range req.AllowedTools {
			if allowed == banned {
				t.Fatalf("security audit without fix intent should stay read-only, got %+v", req.AllowedTools)
			}
		}
	}
}

func TestNormalizeDriveExecution_FixIntentUnlocksWriteTools(t *testing.T) {
	req := NormalizeDriveExecution(drive.ExecuteTodoRequest{
		Role:   "code_reviewer",
		Detail: "Patch the nil handling bug and update the affected call sites.",
	})
	hasPatch := false
	for _, tool := range req.AllowedTools {
		if tool == "apply_patch" {
			hasPatch = true
			break
		}
	}
	if !hasPatch {
		t.Fatalf("fix intent should unlock write tools, got %+v", req.AllowedTools)
	}
}

func TestSelectDriveProfile_PrefersRoleAlignedVendor(t *testing.T) {
	profiles := map[string]config.ModelConfig{
		"google-fast":   {Model: "gemini-3.1-pro-preview-customtools", MaxContext: 500000},
		"anthropic-big": {Model: "claude-sonnet-4-6", MaxContext: 1000000},
		"openai-mid":    {Model: "gpt-5.4", MaxContext: 800000},
	}
	req := NormalizeDriveExecution(drive.ExecuteTodoRequest{
		Role:         "code_reviewer",
		Verification: "deep",
	})
	got := SelectDriveProfile(req, profiles, "google-fast")
	if got != "anthropic-big" {
		t.Fatalf("expected anthropic review profile, got %q", got)
	}
}

func TestSelectDriveProfile_FallsBackToConfiguredProvider(t *testing.T) {
	profiles := map[string]config.ModelConfig{
		"custom-local": {Model: "my-model", MaxContext: 32000},
	}
	req := NormalizeDriveExecution(drive.ExecuteTodoRequest{
		Role: "researcher",
	})
	got := SelectDriveProfile(req, profiles, "custom-local")
	if got != "custom-local" {
		t.Fatalf("expected fallback provider, got %q", got)
	}
}

func TestSelectDriveProfiles_BuildsDeterministicFallbackChain(t *testing.T) {
	profiles := map[string]config.ModelConfig{
		"anthropic-review": {Model: "claude-sonnet-4-6", MaxContext: 1_000_000},
		"openai-fast":      {Model: "gpt-5.4-mini", MaxContext: 800_000},
		"google-wide":      {Model: "gemini-3.1-pro", MaxContext: 900_000},
	}
	req := NormalizeDriveExecution(drive.ExecuteTodoRequest{
		Role:         "code_reviewer",
		Verification: "deep",
	})
	got := SelectDriveProfiles(req, profiles, "openai-fast", 3)
	want := []string{"anthropic-review", "openai-fast", "google-wide"}
	if len(got) != len(want) {
		t.Fatalf("expected %d profiles, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("profile chain mismatch at %d: want %q, got %q (full=%v)", i, want[i], got[i], got)
		}
	}
}

// Explicit role set in request must be preserved through NormalizeDriveExecution.
func TestNormalizeDriveExecution_ExplicitRoleIsPreserved(t *testing.T) {
	req := NormalizeDriveExecution(drive.ExecuteTodoRequest{
		Role:  "debugger",
		Title: "anything",
	})
	if req.Role != "debugger" {
		t.Fatalf("explicit role must not be overwritten, got %q", req.Role)
	}
}

// When role is not set but title contains a keyword, role must be inferred.
func TestNormalizeDriveExecution_RoleInferredFromTitle(t *testing.T) {
	req := NormalizeDriveExecution(drive.ExecuteTodoRequest{
		Title: "Summarize the API surface for the auth package",
	})
	if req.Role != "synthesizer" {
		t.Fatalf("expected synthesizer, got %q", req.Role)
	}
}

// When role is inferred, skills must be auto-added even though not explicitly set.
func TestNormalizeDriveExecution_InferredRoleAutoAddsSkills(t *testing.T) {
	req := NormalizeDriveExecution(drive.ExecuteTodoRequest{
		Title: "Debug the panic in the HTTP handler",
	})
	if req.Role != "debugger" {
		t.Fatalf("expected debugger, got %q", req.Role)
	}
	if len(req.Skills) == 0 {
		t.Fatal("inferred debugger role should auto-add debug skill")
	}
}

// Verification is normalized based on role defaults when not explicitly set.
func TestNormalizeDriveExecution_VerificationDefaultsByRole(t *testing.T) {
	cases := []struct {
		role             string
		wantVerification string
	}{
		{"security_auditor", "deep"},
		{"code_reviewer", "required"},
		{"test_engineer", "required"},
		{"debugger", "required"},
		{"planner", "light"},
		{"researcher", "light"},
		{"documenter", "light"},
	}
	for _, tc := range cases {
		req := NormalizeDriveExecution(drive.ExecuteTodoRequest{
			Role: tc.role,
		})
		if req.Verification != tc.wantVerification {
			t.Errorf("role=%q: want verification=%q, got %q", tc.role, tc.wantVerification, req.Verification)
		}
	}
}

// Explicit verification value is preserved even when it differs from role default.
func TestNormalizeDriveExecution_ExplicitVerificationIsPreserved(t *testing.T) {
	req := NormalizeDriveExecution(drive.ExecuteTodoRequest{
		Role:         "debugger",
		Verification: "light",
	})
	if req.Verification != "light" {
		t.Fatalf("explicit verification must not be overwritten by role default, got %q", req.Verification)
	}
}

// Skills deduplication: auto-added skill must not appear twice.
func TestNormalizeDriveExecution_SkillsDeduplicated(t *testing.T) {
	req := NormalizeDriveExecution(drive.ExecuteTodoRequest{
		Role:   "test_engineer",
		Skills: []string{"test", "review"},
	})
	count := 0
	for _, s := range req.Skills {
		if s == "test" {
			count++
		}
	}
	if count > 1 {
		t.Fatalf("test skill must not be duplicated, got %v", req.Skills)
	}
}

// End-to-end: empty profiles map falls back to fallback string.
func TestSelectDriveProfile_EmptyProfilesFallsBackToFallback(t *testing.T) {
	req := NormalizeDriveExecution(drive.ExecuteTodoRequest{
		Role: "code_reviewer",
	})
	got := SelectDriveProfile(req, nil, "offline-local")
	if got != "offline-local" {
		t.Fatalf("expected fallback offline-local, got %q", got)
	}
}
