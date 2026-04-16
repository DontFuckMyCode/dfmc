package cli

import (
	"strings"
	"testing"
)

func TestRenderCLIHelp_UsesRegistry(t *testing.T) {
	out := renderCLIHelp("")

	// Header is CLI-specific and always present.
	if !strings.HasPrefix(out, "Usage: dfmc [global flags] <command> [args]") {
		t.Fatalf("help must start with CLI usage header; got %q", out[:min(80, len(out))])
	}
	// Category labels from the registry are rendered verbatim.
	for _, label := range []string{"Ask & chat", "Analyze & inspect", "System & meta"} {
		if !strings.Contains(out, label) {
			t.Fatalf("help missing category label %q", label)
		}
	}
	// `serve` is CLI-only — it must appear in CLI help.
	if !strings.Contains(out, "serve") {
		t.Fatalf("CLI-only `serve` should appear in CLI help")
	}
	// Global-flags block is CLI-specific and must be appended after the catalog.
	if !strings.Contains(out, "Global flags:") {
		t.Fatalf("CLI help must include the Global flags section")
	}
	if !strings.Contains(out, `Run "dfmc help <command>"`) {
		t.Fatalf("CLI help must point users at `dfmc help <command>`")
	}
}

func TestRenderCLIHelp_PrependsExtraHeader(t *testing.T) {
	out := renderCLIHelp("DFMC 1.2.3")
	if !strings.HasPrefix(out, "DFMC 1.2.3") {
		t.Fatalf("extra header should lead the output; got %q", out[:40])
	}
	if !strings.Contains(out, "Usage: dfmc") {
		t.Fatalf("usage header must still follow the extra header")
	}
}
