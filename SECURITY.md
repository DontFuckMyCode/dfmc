# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.x.x   | :white_check_mark: |

## Reporting a Vulnerability

If you discover a security vulnerability within DFMC, please follow these steps:

### For Security Researchers

1. **Do NOT** create a public GitHub issue for security vulnerabilities
2. Send a detailed report to the project maintainers via:
   - Email (if private contact is available)
   - GitHub Private Vulnerability Reporting (preferred)

### Report Contents

Your report should include:

- Type of vulnerability (e.g., command injection, path traversal, etc.)
- Full paths of source file(s) related to the vulnerability
- Location of the affected source code (tag/branch or commit)
- Step-by-step instructions to reproduce the issue
- Proof-of-concept or exploit code (if possible)
- Impact assessment of the vulnerability

### What to Expect

- **Acknowledgment**: Within 48 hours
- **Initial Assessment**: Within 7 days
- **Fix Timeline**: Depends on severity (Critical: ASAP, Medium: 30 days)

## Security Best Practices for Users

When using DFMC:

1. **API Keys**: Never commit API keys to version control. Use environment variables or `.env` files.
2. **Editor Access**: Be cautious with editor integrations - they can execute arbitrary code
3. **Plugin Execution**: Review plugin permissions before installation
4. **File Permissions**: Ensure proper file permissions on DFMC configuration files

## Known Security Considerations

### Command Execution

DFMC executes external commands (git, linters, etc.) with user-provided arguments. The codebase:
- Uses `exec.Command` with separate arguments (not shell concatenation)
- Validates paths where possible
- Avoids shell execution unless absolutely necessary

### Data Storage

- SQLite databases are stored locally
- No sensitive data is transmitted to external services (besides LLM API calls)
- API keys are loaded from environment variables or local `.env` files

## Update Policy

Security updates are released as patch versions. We recommend:

- Following the repository for notifications
- Using dependency checking tools (e.g., `govulncheck`)

```bash
# Run security audit
go run golang.org/x/vuln/cmd/govulncheck ./...

# Update dependencies
go get -u ./...
go mod tidy
```

---

*This policy is reviewed quarterly and updated as needed.*
