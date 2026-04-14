package engine

import (
	"context"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestStatusIncludesASTBackend(t *testing.T) {
	cfg := config.DefaultConfig()
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("init engine: %v", err)
	}
	t.Cleanup(func() { eng.Shutdown() })

	st := eng.Status()
	if st.ASTBackend == "" {
		t.Fatal("expected ast backend to be populated")
	}
	if st.ASTReason == "" {
		t.Fatal("expected ast backend reason to be populated")
	}
}
