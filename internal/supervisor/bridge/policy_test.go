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
