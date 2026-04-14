package context

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	if chunks[0].Language == "" {
		t.Fatal("expected language to be populated in context chunk")
	}
}

func TestBuildSystemPromptUsesPromptLibrary(t *testing.T) {
	tmp := t.TempDir()
	mainGo := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(mainGo, []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	ae := ast.New()
	cm := codemap.New(ae)
	if err := cm.BuildFromFiles(context.Background(), []string{mainGo}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}

	mgr := New(cm)
	chunks, err := mgr.Build("security audit", 2)
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	prompt := mgr.BuildSystemPrompt(tmp, "security audit this auth path", chunks)
	if !strings.Contains(strings.ToLower(prompt), "security") {
		t.Fatalf("expected security-focused prompt, got: %s", prompt)
	}
	if !strings.Contains(prompt, tmp) {
		t.Fatalf("expected project root in prompt, got: %s", prompt)
	}
}

func TestBuildSystemPromptInjectsFileMarkerContext(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "auth.go")
	src := `package auth

func VerifyToken(token string) bool {
	return token != ""
}
`
	if err := os.WriteFile(target, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ae := ast.New()
	cm := codemap.New(ae)
	if err := cm.BuildFromFiles(context.Background(), []string{target}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}
	mgr := New(cm)
	query := "please inspect [[file:auth.go#L1-L4]] and explain risks"
	prompt := mgr.BuildSystemPrompt(tmp, query, nil)

	if !strings.Contains(prompt, "[[file:auth.go#L1-L4]]") {
		t.Fatalf("expected injected marker block in prompt, got: %s", prompt)
	}
	if !strings.Contains(prompt, "VerifyToken") {
		t.Fatalf("expected injected code snippet in prompt, got: %s", prompt)
	}
}
