package cli

import "testing"

func TestRenderManOutputs(t *testing.T) {
	docs := commandDocs()
	if len(docs) == 0 {
		t.Fatal("expected command docs")
	}

	md := renderManMarkdown(docs)
	if !contains(md, "# dfmc(1)") || !contains(md, "`analyze`") || !contains(md, "`tui`") {
		t.Fatalf("unexpected markdown output: %s", md)
	}

	roff := renderManRoff(docs)
	if !contains(roff, ".TH DFMC 1") || !contains(roff, ".B analyze") || !contains(roff, ".B tui") {
		t.Fatalf("unexpected roff output: %s", roff)
	}
}
