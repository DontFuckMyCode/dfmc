package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvVarForProvider(t *testing.T) {
	cases := map[string]string{
		"anthropic": "ANTHROPIC_API_KEY",
		"openai":    "OPENAI_API_KEY",
		"deepseek":  "DEEPSEEK_API_KEY",
		"kimi":      "KIMI_API_KEY",
		"minimax":   "MINIMAX_API_KEY",
		"zai":       "ZAI_API_KEY",
		"alibaba":   "ALIBABA_API_KEY",
		"google":    "GOOGLE_AI_API_KEY",
		"Anthropic": "ANTHROPIC_API_KEY", // case-insensitive
		" openai ":  "OPENAI_API_KEY",    // whitespace-tolerant
	}
	for in, want := range cases {
		if got := EnvVarForProvider(in); got != want {
			t.Errorf("EnvVarForProvider(%q) = %q; want %q", in, got, want)
		}
	}
	if got := EnvVarForProvider("generic"); got != "" {
		t.Errorf("EnvVarForProvider(generic) = %q; want empty (no canonical env var)", got)
	}
	if got := EnvVarForProvider(""); got != "" {
		t.Errorf("EnvVarForProvider(empty) = %q; want empty", got)
	}
}

func TestLoadWithOptions_MergeAndEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-anthropic-key")

	tmp := t.TempDir()
	globalPath := filepath.Join(tmp, "global.yaml")
	projectRoot := filepath.Join(tmp, "project")
	projectPath := filepath.Join(projectRoot, ".dfmc", "config.yaml")

	if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
		t.Fatalf("mkdir project config dir: %v", err)
	}

	globalYAML := []byte(`
version: 1
providers:
  primary: openai
`)
	if err := os.WriteFile(globalPath, globalYAML, 0o644); err != nil {
		t.Fatalf("write global: %v", err)
	}

	projectYAML := []byte(`
version: 1
web:
  port: 8800
`)
	if err := os.WriteFile(projectPath, projectYAML, 0o644); err != nil {
		t.Fatalf("write project: %v", err)
	}

	cfg, err := LoadWithOptions(LoadOptions{
		GlobalPath:  globalPath,
		ProjectPath: projectPath,
		CWD:         projectRoot,
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Providers.Primary != "openai" {
		t.Fatalf("expected primary openai, got %s", cfg.Providers.Primary)
	}
	if cfg.Web.Port != 8800 {
		t.Fatalf("expected web port 8800, got %d", cfg.Web.Port)
	}

	p := cfg.Providers.Profiles["anthropic"]
	if p.APIKey != "test-anthropic-key" {
		t.Fatalf("expected anthropic key from env, got %q", p.APIKey)
	}
}

func TestLoadWithOptions_LoadsProjectDotEnv(t *testing.T) {
	// Save and clear any pre-existing ZAI_API_KEY so .env is the sole source.
	prevZAI, hadZAI := os.LookupEnv("ZAI_API_KEY")
	os.Unsetenv("ZAI_API_KEY")
	t.Cleanup(func() {
		if hadZAI {
			os.Setenv("ZAI_API_KEY", prevZAI)
		} else {
			os.Unsetenv("ZAI_API_KEY")
		}
	})

	tmp := t.TempDir()
	projectRoot := filepath.Join(tmp, "project")
	projectPath := filepath.Join(projectRoot, ".dfmc", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
		t.Fatalf("mkdir project config dir: %v", err)
	}
	if err := os.WriteFile(projectPath, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".env"), []byte("ZAI_API_KEY=dotenv-zai-key\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	cfg, err := LoadWithOptions(LoadOptions{
		ProjectPath: projectPath,
		CWD:         projectRoot,
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got := cfg.Providers.Profiles["zai"].APIKey; got != "dotenv-zai-key" {
		t.Fatalf("expected zai key from .env, got %q", got)
	}
}

func TestLoadWithOptions_ProcessEnvOverridesDotEnv(t *testing.T) {
	t.Setenv("ZAI_API_KEY", "process-zai-key")

	tmp := t.TempDir()
	projectRoot := filepath.Join(tmp, "project")
	projectPath := filepath.Join(projectRoot, ".dfmc", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
		t.Fatalf("mkdir project config dir: %v", err)
	}
	if err := os.WriteFile(projectPath, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".env"), []byte("ZAI_API_KEY=dotenv-zai-key\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	cfg, err := LoadWithOptions(LoadOptions{
		ProjectPath: projectPath,
		CWD:         projectRoot,
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got := cfg.Providers.Profiles["zai"].APIKey; got != "process-zai-key" {
		t.Fatalf("expected process env to win over .env, got %q", got)
	}
}

func TestLoadWithOptions_DotEnvDoesNotMutateProcessEnv(t *testing.T) {
	os.Unsetenv("OPENAI_API_KEY")
	tmp := t.TempDir()
	projectRoot := filepath.Join(tmp, "project")
	projectPath := filepath.Join(projectRoot, ".dfmc", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
		t.Fatalf("mkdir project config dir: %v", err)
	}
	if err := os.WriteFile(projectPath, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".env"), []byte("OPENAI_API_KEY=dotenv-openai-key\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	cfg, err := LoadWithOptions(LoadOptions{
		ProjectPath: projectPath,
		CWD:         projectRoot,
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := cfg.Providers.Profiles["openai"].APIKey; got != "dotenv-openai-key" {
		t.Fatalf("expected openai key from .env, got %q", got)
	}
	if got := os.Getenv("OPENAI_API_KEY"); got != "" {
		t.Fatalf(".env load must not mutate process env, got %q", got)
	}
}

func TestLoadWithOptions_StripsProjectHooksByDefault(t *testing.T) {
	tmp := t.TempDir()
	globalPath := filepath.Join(tmp, "global.yaml")
	projectRoot := filepath.Join(tmp, "project")
	projectPath := filepath.Join(projectRoot, ".dfmc", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
		t.Fatalf("mkdir project config dir: %v", err)
	}
	if err := os.WriteFile(globalPath, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}
	projectYAML := []byte(`
version: 1
hooks:
  session_start:
    - name: repo-hook
      command: echo from-project
`)
	if err := os.WriteFile(projectPath, projectYAML, 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	cfg, err := LoadWithOptions(LoadOptions{
		GlobalPath:  globalPath,
		ProjectPath: projectPath,
		CWD:         projectRoot,
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Hooks.AllowProject {
		t.Fatal("project hooks should remain disabled by default")
	}
	if len(cfg.Hooks.Entries) != 0 {
		t.Fatalf("expected project hooks to be stripped by default, got %#v", cfg.Hooks.Entries)
	}
}

func TestLoadWithOptions_AllowsProjectHooksWhenGlobalOptIn(t *testing.T) {
	tmp := t.TempDir()
	globalPath := filepath.Join(tmp, "global.yaml")
	projectRoot := filepath.Join(tmp, "project")
	projectPath := filepath.Join(projectRoot, ".dfmc", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
		t.Fatalf("mkdir project config dir: %v", err)
	}
	globalYAML := []byte(`
version: 1
hooks:
  allow_project: true
`)
	if err := os.WriteFile(globalPath, globalYAML, 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}
	projectYAML := []byte(`
version: 1
hooks:
  session_start:
    - name: repo-hook
      command: echo from-project
`)
	if err := os.WriteFile(projectPath, projectYAML, 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	cfg, err := LoadWithOptions(LoadOptions{
		GlobalPath:  globalPath,
		ProjectPath: projectPath,
		CWD:         projectRoot,
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.Hooks.AllowProject {
		t.Fatal("expected global opt-in to allow project hooks")
	}
	if got := len(cfg.Hooks.Entries["session_start"]); got != 1 {
		t.Fatalf("expected 1 project hook after opt-in, got %d", got)
	}
	if got := cfg.Hooks.Entries["session_start"][0].Name; got != "repo-hook" {
		t.Fatalf("expected repo-hook, got %q", got)
	}
}

func TestFindProjectRoot(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "repo")
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	got := FindProjectRoot(nested)
	if got != root {
		t.Fatalf("expected %s, got %s", root, got)
	}
}

func TestValidate_ContextMaxTokensTotalMustBePositive(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Context.MaxTokensTotal = 0

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for context.max_tokens_total <= 0")
	}
}

func TestValidate_ContextMaxHistoryTokensMustBePositive(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Context.MaxHistoryTokens = 0

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for context.max_history_tokens <= 0")
	}
}
