package tui

import (
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func TestTUIGitDiffTimeoutUsesDefaultForNilEngine(t *testing.T) {
	if got := tuiGitDiffTimeout(nil); got != defaultGitDiffTimeout {
		t.Fatalf("nil engine timeout = %s, want %s", got, defaultGitDiffTimeout)
	}
}

func TestTUIGitDiffTimeoutUsesConfigOverride(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.TUI.GitDiffTimeoutSeconds = 7
	eng := &engine.Engine{Config: cfg}

	if got := tuiGitDiffTimeout(eng); got != 7*time.Second {
		t.Fatalf("configured timeout = %s, want 7s", got)
	}
}
