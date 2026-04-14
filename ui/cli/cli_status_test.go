package cli

import (
	"testing"
)

func TestRunStatusJSON(t *testing.T) {
	eng := newCLITestEngine(t)
	if code := runStatus(eng, "dev", []string{"--query", "security audit auth middleware"}, true); code != 0 {
		t.Fatalf("runStatus json exit=%d", code)
	}
}

func TestRunStatusText(t *testing.T) {
	eng := newCLITestEngine(t)
	if code := runStatus(eng, "dev", []string{}, false); code != 0 {
		t.Fatalf("runStatus text exit=%d", code)
	}
}
