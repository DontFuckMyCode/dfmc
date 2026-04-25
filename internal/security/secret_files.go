package security

// secret_files.go — shared classifier for path leaves that almost
// certainly hold credentials, private keys, or other "shouldn't be
// served verbatim" material. Used by:
//
//   - the TUI's preview redactor (ui/tui) — refuses to read the file
//     into the on-screen panel
//   - the web file API (ui/web) — refuses to return raw bytes via
//     `GET /api/v1/files/{path...}`
//   - any future tool that surfaces user-facing file content
//
// Detection is intentionally over-eager: a false positive means the
// caller substitutes a redacted placeholder, which is recoverable
// (the user can read the file with their own tools); a false negative
// publishes a credential. The asymmetry is the whole design.
//
// LooksLikeSecretFile was promoted from ui/tui/secret_redact.go after
// VULN-013 — the web file API was serving `.env`, `id_rsa`,
// `credentials.json` verbatim because the predicate lived behind a
// TUI-only symbol. Anything that hands raw bytes to a remote/network
// surface should consult this function first.

import (
	"path/filepath"
	"strings"
)

// LooksLikeSecretFile returns true for paths that almost certainly
// hold credentials, private keys, or other shouldn't-be-on-screen
// material. Caller substitutes a redacted notice for the real bytes
// when this returns true.
//
// Categories matched:
//
//   - Exact basenames: .env, .envrc, .netrc, .pgpass, id_rsa,
//     credentials.json, secrets.yaml, htpasswd, service-account.json,
//     etc.
//   - Anything starting with `.env.` (.env.local, .env.production, …)
//   - Extensions: .pem, .key, .p12, .pfx, .kdbx, .jks, .keystore,
//     .der, .gpg
//   - Substring heuristics: secret, credential, password, apikey,
//     private_key — noisy on purpose; cheaper to redact a
//     `password_reset_flow.md` doc than to leak `passwords.json`.
func LooksLikeSecretFile(rel string) bool {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return false
	}
	base := strings.ToLower(filepath.Base(rel))
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
	if strings.HasPrefix(base, ".env.") {
		return true
	}
	for _, ext := range []string{
		".pem", ".key", ".p12", ".pfx", ".kdbx",
		".jks", ".keystore", ".der", ".gpg",
	} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
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
