# sc-secrets Results

## Findings

### [Medium] Live API Keys in `.env` file

- **File**: `.env:8, 11, 29`
- **Description**: The `.env` file contains live `ZAI_API_KEY`, `MINIMAX_API_KEY`, and `KIMI_API_KEY` values with real provider-specific key formats (hex-dot, `sk-cp-`, `sk-kimi-` prefixes).
- **Impact**: If `.env` is ever accidentally committed to version control, these credentials would be exposed. Currently gitignored, but local file permissions are the only protection at rest.
- **Evidence**:
  - `.env:8` — `ZAI_API_KEY` with hex-dot format
  - `.env:11` — `MINIMAX_API_KEY` with `sk-cp-` prefix
  - `.env:29` — `KIMI_API_KEY` with `sk-kimi-` prefix
  - `.env.example` uses placeholder syntax, confirming real credentials in `.env`
- **Mitigation**:
  1. Verify `.env` is truly in `.gitignore` and stays there
  2. Rotate these keys if they've been shared or exposed
  3. Consider using a secrets manager instead of `.env` for production deployments

---

## No Issues Found

### Go Source Files
- All API key references in Go source use `os.Getenv` at runtime — no hardcoded values
- Test fixtures use clearly fake values (`sk-abcdefghijklmnopqrstuvwxyz1234567890`)

### Secret Scrubbing
- `hooks.go:247` calls `security.ScrubEnv(os.Environ(), nil)` before passing env to subprocess
- `ScrubEnv` strips keys matching `*_API_KEY`, `*_TOKEN`, `*_SECRET`, `AWS_SECRET_ACCESS_KEY`, etc.

### Other Secret Patterns
- No embedded Git credentials in URLs
- No PEM private keys in source
- No basic-auth base64 strings in URLs
- `os.Getenv` calls always read at runtime, never hardcode values
- EventBus redaction and config output redaction verified correct