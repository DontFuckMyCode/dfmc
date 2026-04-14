package context

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

func TestBuildContextFromQuery(t *testing.T) {
	tmp := t.TempDir()
	mainGo := filepath.Join(tmp, "main.go")
	authGo := filepath.Join(tmp, "auth.go")

	if err := os.WriteFile(mainGo, []byte(`package main
import "fmt"
func main(){ fmt.Println("ok") }`), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(authGo, []byte(`package main
type AuthService struct {}
func VerifyToken(token string) bool { return token != "" }`), 0o644); err != nil {
		t.Fatalf("write auth.go: %v", err)
	}

	ae := ast.New()
	cm := codemap.New(ae)
	if err := cm.BuildFromFiles(context.Background(), []string{mainGo, authGo}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}

	mgr := New(cm)
	chunks, err := mgr.Build("token auth verification", 3)
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one context chunk")
	}
}
