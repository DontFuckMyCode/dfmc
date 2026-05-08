package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// keyTestModel builds a TUI Model whose user-config path lives under
// a temp dir, so /key set doesn't touch the real ~/.dfmc/config.yaml.
// HOME / USERPROFILE redirect ensures config.UserConfigDir() resolves
// into the temp dir on both Windows and POSIX hosts.
func keyTestModel(t *testing.T) (Model, string) {
	t.Helper()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	projectRoot := t.TempDir()
	cfg := config.DefaultConfig()
	eng := &engine.Engine{
		Config:      cfg,
		ProjectRoot: projectRoot,
		EventBus:    engine.NewEventBus(),
	}
	m := NewModel(context.Background(), eng)
	return m, projectRoot
}

func TestRunKeyCommand_SetWritesUserConfig(t *testing.T) {
	m, _ := keyTestModel(t)
	_, _, handled := m.runKeyCommand([]string{"set", "anthropic", "sk-ant-api03-secret"})
	if !handled {
		t.Fatalf("expected /key set to be handled")
	}
	path, err := m.userConfigPath()
	if err != nil {
		t.Fatalf("userConfigPath: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse written yaml: %v", err)
	}
	got := readNestedString(t, doc, "providers", "profiles", "anthropic", "api_key")
	if got != "sk-ant-api03-secret" {
		t.Errorf("api_key not persisted: got %q", got)
	}
}

func TestRunKeyCommand_SetUpdatesEngineInMemory(t *testing.T) {
	m, _ := keyTestModel(t)
	if _, _, handled := m.runKeyCommand([]string{"set", "openai", "sk-openai-x"}); !handled {
		t.Fatalf("expected /key set to be handled")
	}
	got := m.eng.Config.Providers.Profiles["openai"].APIKey
	if got != "sk-openai-x" {
		t.Errorf("engine config api_key: got %q want %q", got, "sk-openai-x")
	}
}

func TestRunKeyCommand_SetRejectsUnknownProvider(t *testing.T) {
	m, _ := keyTestModel(t)
	_, _, handled := m.runKeyCommand([]string{"set", "fakecorp", "key"})
	if !handled {
		t.Fatalf("expected handled=true even on unknown provider")
	}
	// Verify nothing was written.
	path, _ := m.userConfigPath()
	if _, err := os.Stat(path); err == nil {
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), "fakecorp") {
			t.Errorf("unknown provider was written: %s", data)
		}
	}
}

func TestRunKeyCommand_SetRequiresArgs(t *testing.T) {
	m, _ := keyTestModel(t)
	if _, _, handled := m.runKeyCommand([]string{"set"}); !handled {
		t.Fatalf("expected /key set with no args to be handled (with usage)")
	}
	if _, _, handled := m.runKeyCommand([]string{"set", "anthropic"}); !handled {
		t.Fatalf("expected /key set with only provider to be handled (with usage)")
	}
}

func TestRunKeyCommand_ClearRemovesField(t *testing.T) {
	m, _ := keyTestModel(t)
	if _, _, handled := m.runKeyCommand([]string{"set", "deepseek", "sk-deep-secret"}); !handled {
		t.Fatalf("seed: /key set should be handled")
	}
	if _, _, handled := m.runKeyCommand([]string{"clear", "deepseek"}); !handled {
		t.Fatalf("expected /key clear to be handled")
	}
	keys := m.readUserConfigAPIKeys()
	if _, ok := keys["deepseek"]; ok {
		t.Errorf("deepseek key still present after /key clear: %v", keys)
	}
	if got := m.eng.Config.Providers.Profiles["deepseek"].APIKey; got != "" {
		t.Errorf("engine still has key after clear: %q", got)
	}
}

func TestRunKeyCommand_MigrateCopiesDotEnvKeys(t *testing.T) {
	m, projectRoot := keyTestModel(t)
	dotEnvPath := filepath.Join(projectRoot, ".env")
	dotEnv := strings.Join([]string{
		"# project keys",
		`ANTHROPIC_API_KEY="sk-ant-from-dotenv"`,
		"OPENAI_API_KEY=sk-openai-from-dotenv",
		"GARBAGE_VAR=ignored",
	}, "\n")
	if err := os.WriteFile(dotEnvPath, []byte(dotEnv), 0o600); err != nil {
		t.Fatalf("seed .env: %v", err)
	}
	if _, _, handled := m.runKeyCommand([]string{"migrate"}); !handled {
		t.Fatalf("expected /key migrate to be handled")
	}
	keys := m.readUserConfigAPIKeys()
	if keys["anthropic"] != "sk-ant-from-dotenv" {
		t.Errorf("anthropic not migrated: got %q", keys["anthropic"])
	}
	if keys["openai"] != "sk-openai-from-dotenv" {
		t.Errorf("openai not migrated: got %q", keys["openai"])
	}
}

func TestRunKeyCommand_MigrateDoesNotOverwriteExisting(t *testing.T) {
	m, projectRoot := keyTestModel(t)
	if _, _, handled := m.runKeyCommand([]string{"set", "anthropic", "user-home-key"}); !handled {
		t.Fatalf("seed user-home key: handled expected")
	}
	dotEnv := `ANTHROPIC_API_KEY=dotenv-key`
	if err := os.WriteFile(filepath.Join(projectRoot, ".env"), []byte(dotEnv), 0o600); err != nil {
		t.Fatalf("seed .env: %v", err)
	}
	if _, _, handled := m.runKeyCommand([]string{"migrate"}); !handled {
		t.Fatalf("expected /key migrate to be handled")
	}
	keys := m.readUserConfigAPIKeys()
	if keys["anthropic"] != "user-home-key" {
		t.Errorf("user-home key was overwritten by .env: got %q", keys["anthropic"])
	}
}

func TestMaskAPIKey(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "—"},
		{"abcd", "••••"},
		{"sk-ant-api03-XXXX-1234", "••••••••••••1234"},
		{"abcde", "•bcde"},
	}
	for _, tc := range cases {
		if got := maskAPIKey(tc.in); got != tc.want {
			t.Errorf("maskAPIKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestProviderForEnvVar(t *testing.T) {
	cases := map[string]string{
		"ANTHROPIC_API_KEY": "anthropic",
		"OPENAI_API_KEY":    "openai",
		"KIMI_API_KEY":      "kimi",
		"MOONSHOT_API_KEY":  "kimi",
		"GARBAGE_KEY":       "",
		"":                  "",
	}
	for in, want := range cases {
		if got := providerForEnvVar(in); got != want {
			t.Errorf("providerForEnvVar(%q) = %q, want %q", in, got, want)
		}
	}
}

func readNestedString(t *testing.T, doc map[string]any, path ...string) string {
	t.Helper()
	cur := any(doc)
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("path %v: expected map at %q, got %T", path, k, cur)
		}
		cur, ok = m[k]
		if !ok {
			return ""
		}
	}
	s, _ := cur.(string)
	return s
}
