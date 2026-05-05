package engine

import (
	"runtime"
	"strings"
	"testing"
)

func TestToolReasoningSystemNoticeIsExplicitlyRequired(t *testing.T) {
	notice := toolReasoningSystemNotice()
	for _, want := range []string{"REQUIRED", "`_reason`", "Treat missing `_reason` as an invalid tool-call shape", "batch calls"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("tool reasoning notice missing %q: %s", want, notice)
		}
	}
}

func TestHostOSSystemNoticeClarifiesWindowsPaths(t *testing.T) {
	notice := hostOSSystemNotice()
	if runtime.GOOS != "windows" {
		if !strings.Contains(notice, "no shell") {
			t.Fatalf("non-windows host notice should still explain shell boundary: %s", notice)
		}
		return
	}
	for _, want := range []string{"Windows", "absolute path", "forward slashes", "escaped backslashes"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("windows host notice missing %q: %s", want, notice)
		}
	}
}
