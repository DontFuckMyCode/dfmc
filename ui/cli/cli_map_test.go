package cli

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

func TestGraphToSVG(t *testing.T) {
	nodes := []codemap.Node{
		{ID: "n1", Name: "AuthService", Kind: "type"},
		{ID: "n2", Name: "Login", Kind: "function"},
	}
	edges := []codemap.Edge{
		{From: "n1", To: "n2", Type: "calls"},
	}
	out := graphToSVG(nodes, edges)
	if !strings.Contains(out, "<svg") || !strings.Contains(out, "</svg>") {
		t.Fatalf("expected svg wrapper, got: %s", out)
	}
	if !strings.Contains(out, "AuthService") || !strings.Contains(out, "Login") {
		t.Fatalf("expected node labels in svg, got: %s", out)
	}
	if !strings.Contains(out, "<line") {
		t.Fatalf("expected edge line in svg, got: %s", out)
	}
}
