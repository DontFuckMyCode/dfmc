package tui

// Pins the redaction shape for the Files panel preview. Without these
// tests we can't be sure a renamed `.env` (e.g. `.env.production`) or
// a relocated `id_rsa` would still trigger the suppression — the bug
// these guard against is silent: a pretty preview that happens to
// contain ZAI_API_KEY=...

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLooksLikeSecretFile_KnownShapes(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		// Canonical env files — including dot-suffixed variants.
		{"dot env", ".env", true},
		{"dot env local", ".env.local", true},
		{"dot env production", ".env.production", true},
		{"dot env example", ".env.example", true},
		{"envrc", ".envrc", true},
		// SSH private keys.
		{"id_rsa", "id_rsa", true},
		{"id_ed25519", "id_ed25519", true},
		{"nested id_rsa", "config/keys/id_rsa", true},
		// Cert/key extensions.
		{"pem cert", "certs/server.pem", true},
		{"key file", "secrets/api.key", true},
		{"p12", "client.p12", true},
		{"pfx", "client.pfx", true},
		{"keystore", "android.keystore", true},
		// Substring catches.
		{"secrets json", "config/secrets.json", true},
		{"credentials yaml", "infra/credentials.yaml", true},
		{"password file", "passwords.txt", true},
		{"api_key file", "api_keys.txt", true},
		// Negatives — must not trigger or the panel becomes useless.
		{"go file", "main.go", false},
		{"readme", "README.md", false},
		{"gitignore", ".gitignore", false},
		{"yaml without secret", "config.yaml", false},
		{"json without secret", "data.json", false},
		{"public key", "keys/public.txt", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := looksLikeSecretFile(c.path); got != c.want {
				t.Fatalf("looksLikeSecretFile(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

// readProjectFile must NOT return the bytes of a .env file even when it
// exists on disk and contains real-looking key=value lines. Regression:
// previously the Files panel showed the contents in plain text the
// instant the tab opened, exposing every API key to whoever was looking.
func TestReadProjectFile_RedactsDotEnvContents(t *testing.T) {
	dir := t.TempDir()
	body := "ZAI_API_KEY=82ebd5d747cc4a559307f721b7e39be0.11So7iL4tmEqg4H7\n" +
		"MINIMAX_API_KEY=sk-cp-VV4DyTLLkq558U1-w3l-678o6uYJmAalFhHIZtF9xNuP42SA59ygKacvgFyvD\n" +
		"ALIBABA_API_KEY=sk-sp-377d1df51a7d49e6a38a2d02240e6b45\n"
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	content, size, err := readProjectFile(dir, ".env", 32_000)
	if err != nil {
		t.Fatalf("readProjectFile error: %v", err)
	}
	if size != len(body) {
		t.Fatalf("size mismatch: got %d, want %d", size, len(body))
	}
	for _, secret := range []string{
		"ZAI_API_KEY", "MINIMAX_API_KEY", "ALIBABA_API_KEY",
		"82ebd5d747cc4a559307f721b7e39be0",
		"sk-cp-VV4DyTLLkq558U1",
		"sk-sp-377d1df51a7d49e6a38a2d02240e6b45",
	} {
		if strings.Contains(content, secret) {
			t.Fatalf("preview leaked secret %q:\n%s", secret, content)
		}
	}
	if !strings.Contains(content, "Preview suppressed") {
		t.Fatalf("redaction notice missing:\n%s", content)
	}
}

// A non-secret file in the same directory must still preview normally —
// regression guard against an over-eager rule that accidentally
// silences README.md too.
func TestReadProjectFile_StillPreviewsNonSecretFiles(t *testing.T) {
	dir := t.TempDir()
	body := "# DFMC\n\nHello world.\n"
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	content, _, err := readProjectFile(dir, "README.md", 32_000)
	if err != nil {
		t.Fatalf("readProjectFile error: %v", err)
	}
	if !strings.Contains(content, "Hello world") {
		t.Fatalf("README contents missing from preview:\n%s", content)
	}
}
