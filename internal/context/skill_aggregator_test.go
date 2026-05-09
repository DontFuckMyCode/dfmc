package context

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/skills"
)

func TestSummarizeActiveSkills_Empty(t *testing.T) {
	got := summarizeActiveSkills(nil, nil)
	if got != "(none active)" {
		t.Fatalf("expected '(none active)', got %q", got)
	}
}

func TestSummarizeActiveSkills_NoOrigin(t *testing.T) {
	active := []skills.Skill{{Name: "review"}, {Name: "audit"}}
	got := summarizeActiveSkills(active, nil)
	if got != "review, audit" {
		t.Fatalf("expected 'review, audit', got %q", got)
	}
}

func TestSummarizeActiveSkills_WithOrigin(t *testing.T) {
	active := []skills.Skill{{Name: "audit"}, {Name: "onboard"}}
	origin := map[string]string{
		"audit":   "trigger",
		"onboard": "required",
	}
	got := summarizeActiveSkills(active, origin)
	want := "audit (trigger), onboard (required)"
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestSummarizeActiveSkills_OriginPartialMissing(t *testing.T) {
	// Skill present in active but missing from origin map: render
	// without the badge rather than an empty parens.
	active := []skills.Skill{{Name: "review"}, {Name: "audit"}}
	origin := map[string]string{"audit": "trigger"}
	got := summarizeActiveSkills(active, origin)
	if !strings.Contains(got, "audit (trigger)") {
		t.Errorf("expected 'audit (trigger)' in output, got %q", got)
	}
	if strings.Contains(got, "review (") {
		t.Errorf("review has no origin entry; should not render parens, got %q", got)
	}
}

func TestSummarizeActiveSkills_BadgeKeyIsLowercased(t *testing.T) {
	// Origin keys are lowercased; skill names may be mixed-case.
	// The lookup must normalise.
	active := []skills.Skill{{Name: "Review"}}
	origin := map[string]string{"review": "explicit"}
	got := summarizeActiveSkills(active, origin)
	if got != "Review (explicit)" {
		t.Fatalf("expected case-preserved name with lowercased lookup, got %q", got)
	}
}
