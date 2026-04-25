package security

import "testing"

// TestLooksLikeSecretFile mirrors the table from the original TUI
// test (ui/tui/secret_redact_test.go) so the promote-to-shared move
// doesn't silently shrink the matched set.
func TestLooksLikeSecretFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Exact basenames
		{".env", true},
		{".envrc", true},
		{".env.example", true},
		{"id_rsa", true},
		{"id_ed25519", true},
		{"credentials.json", true},
		{"secrets.yaml", true},
		{"htpasswd", true},
		{".htpasswd", true},
		{"service-account.json", true},
		{"private.key", true},
		{"private_key.pem", true},

		// .env.<anything>
		{".env.local", true},
		{".env.production", true},
		{".env.test", true},
		{"foo/.env.staging", true},

		// Extensions
		{"my-cert.pem", true},
		{"server.key", true},
		{"keystore.jks", true},
		{"vault.kdbx", true},
		{"backup.gpg", true},

		// Substring heuristics — over-eager on purpose.
		{"path/to/passwords.txt", true},
		{"my_secret.go", true},
		{"docs/api_key_setup.md", true},
		{"private_key_loader.go", true},

		// Non-matches
		{"main.go", false},
		{"README.md", false},
		{"src/foo.ts", false},
		{"", false},

		// Path forms — predicate looks at basename only.
		{"sub/dir/.env", true},
		{"deep/nest/credentials.json", true},
	}
	for _, c := range cases {
		got := LooksLikeSecretFile(c.path)
		if got != c.want {
			t.Errorf("LooksLikeSecretFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
