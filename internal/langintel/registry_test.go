package langintel

import (
	"testing"
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if len(r.Practices) == 0 {
		t.Fatal("expected practices in registry")
	}
	if len(r.BugPatterns) == 0 {
		t.Fatal("expected bug patterns in registry")
	}
	if len(r.SecurityRules) == 0 {
		t.Fatal("expected security rules in registry")
	}
}

func TestForLang(t *testing.T) {
	r := NewRegistry()
	goReg := r.ForLang("go")
	if len(goReg.Practices) == 0 {
		t.Error("expected Go practices")
	}
	if len(goReg.BugPatterns) == 0 {
		t.Error("expected Go bug patterns")
	}
}

func TestForKinds(t *testing.T) {
	r := NewRegistry()
	reg := r.ForKinds([]string{"call_expression"})
	if len(reg.Practices) == 0 {
		t.Error("expected practices for call_expression")
	}
}

func TestBestPracticesFor(t *testing.T) {
	r := NewRegistry()
	tips := r.BestPracticesFor([]string{"if_statement"}, 3)
	if len(tips) == 0 {
		t.Fatal("expected tips for if_statement")
	}
	if len(tips) > 3 {
		t.Errorf("expected at most 3 tips, got %d", len(tips))
	}
}

func TestNormalizeLang(t *testing.T) {
	cases := []struct{ in, want string }{
		{"go", "go"}, {"golang", "go"}, {"typescript", "typescript"},
		{"ts", "typescript"}, {"python", "python"}, {"rust", "rust"},
	}
	for _, c := range cases {
		got := NormalizeLang(c.in)
		if got != c.want {
			t.Errorf("NormalizeLang(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMerge(t *testing.T) {
	r1 := NewRegistry()
	r2 := &Registry{
		Practices: []Practice{{ID: "new-practice", Summary: "new", Langs: []string{"go"}}},
	}
	merged := r1.Merge(r2)
	if len(merged.Practices) != len(r1.Practices)+1 {
		t.Errorf("expected merged practices to have one more entry")
	}
}

func TestIdiomsFor(t *testing.T) {
	r := NewRegistry()
	idioms := r.IdiomsFor("go")
	if len(idioms) == 0 {
		t.Error("expected Go idioms")
	}
	for _, i := range idioms {
		if i.Lang != "go" && i.Lang != "" {
			t.Errorf("unexpected non-Go idiom: %s (lang=%s)", i.ID, i.Lang)
		}
	}
}
