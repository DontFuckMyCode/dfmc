package tui

// secret_redact.go — preview-time guard against leaking secret-shaped
// files (.env, *.pem, id_rsa, ...) into the Files panel. The screen is
// often shared (paired-programming, screenshots, screen-share, demos),
// so a single auto-preview of `.env` is enough to publish API keys to
// the world. We refuse to read the bytes off disk for anything whose
// path/basename matches a well-known secret shape.
//
// Detection is intentionally over-eager: a false positive shows the
// user a "🔒 preview suppressed" line instead of the contents — the
// file remains in the list, the user can still copy it to chat with
// explicit consent, and the size is still reported. A false negative
// leaks a credential.

import (
	"path/filepath"
	"strings"
)

// looksLikeSecretFile returns true for paths that almost certainly
// hold credentials, private keys, or other shouldn't-be-on-screen
// material. Caller (readProjectFile) substitutes a notice for the
// real bytes when this returns true.
func looksLikeSecretFile(rel string) bool {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return false
	}
	base := strings.ToLower(filepath.Base(rel))
	// Exact basenames — the canonical secret-bearing files.
	switch base {
	case ".env", ".envrc", ".env.example",
		".netrc", ".pgpass", ".npmrc", ".pypirc",
		"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
		"credentials", "credentials.json", "credentials.yaml", "credentials.yml",
		"secrets.json", "secrets.yaml", "secrets.yml",
		"htpasswd", ".htpasswd", "service-account.json",
		"private.key", "private_key.pem":
		return true
	}
	// .env.<anything> — covers .env.local, .env.production, .env.test, ...
	// .env.example is an exact-name carve-out above (still redacted —
	// example files often ship with real keys by mistake).
	if strings.HasPrefix(base, ".env.") {
		return true
	}
	// Extensions that almost always carry private material.
	for _, ext := range []string{
		".pem", ".key", ".p12", ".pfx", ".kdbx",
		".jks", ".keystore", ".der", ".gpg",
	} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	// Heuristic substrings — "secret", "credential", "password", "apikey".
	// These are noisy on purpose; cheaper to refuse a docs page named
	// password_reset_flow.md than to leak passwords.json.
	for _, needle := range []string{
		"secret", "secrets",
		"credential", "credentials",
		"password", "passwords",
		"apikey", "api_key", "api-key",
		"private_key", "privatekey",
	} {
		if strings.Contains(base, needle) {
			return true
		}
	}
	return false
}
