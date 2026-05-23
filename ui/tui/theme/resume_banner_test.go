package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// resume_banner_test.go pins the parking-banner key hint against drift.
// The banner used to advertise `ctrl+x resumes`, but the chat composer
// also routes Enter through submitChatComposer → startChatResume — and
// every other surface (composer hint, starter tips) names `enter` as
// the primary submit key. Listing only ctrl+x was a discoverability
// regression for users who reach for Enter first.

func TestResumeBanner_NamesEnterAsResume(t *testing.T) {
	out := ansi.Strip(RenderResumeBanner(3, 60, 80))
	if !strings.Contains(out, "enter resumes") {
		t.Errorf("parking banner must name enter as the resume key, got:\n%s", out)
	}
	if !strings.Contains(out, "esc dismisses") {
		t.Errorf("parking banner must keep esc as the dismiss key, got:\n%s", out)
	}
	if !strings.Contains(out, "/continue") {
		t.Errorf("parking banner must reference /continue as the steer path, got:\n%s", out)
	}
}
