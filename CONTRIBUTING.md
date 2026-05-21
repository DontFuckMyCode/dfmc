# Contributing to DFMC

Thank you for your interest in contributing to DFMC! This document provides guidelines and instructions for contributing.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [Making Changes](#making-changes)
- [Testing](#testing)
- [Pull Request Process](#pull-request-process)
- [Style Guide](#style-guide)

## Code of Conduct

This project adheres to a code of conduct that all contributors are expected to follow. Please be respectful and constructive in all interactions.

## Getting Started

1. Fork the repository
2. Clone your fork locally
3. Add the upstream remote:
   ```bash
   git remote add upstream https://github.com/dontfuckmycode/dfmc.git
   ```

## Development Setup

### Prerequisites

- Go 1.21+
- Git
- SQLite (for conversation storage)
- Optional: Node.js (for UI tooling)

### Initial Setup

```bash
# Clone your fork
git clone https://github.com/YOUR_USERNAME/dfmc.git
cd dfmc

# Install dependencies
go mod download

# Copy environment template
cp .env.example .env
# Edit .env with your API keys

# Build the project
go build ./...

# Run tests
go test ./...
```

## Making Changes

### Branch Naming

Use descriptive branch names:

- `feature/description` - New features
- `fix/description` - Bug fixes
- `refactor/description` - Code refactoring
- `docs/description` - Documentation updates
- `security/description` - Security-related changes

### Keeping Your Fork Updated

```bash
# Fetch from upstream
git fetch upstream

# Merge latest into your branch
git checkout main
git merge upstream/main
```

## Testing

### Running Tests

```bash
# Run all tests
go test ./...

# Run with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html

# Run with race detector
go test -race ./...

# Run specific package tests
go test ./internal/engine/...
```

### Writing Tests

- Aim for meaningful test coverage
- Test edge cases and error conditions
- Use table-driven tests when appropriate
- Follow existing test patterns in the codebase

### Test File Naming

```go
// For mypackage.go, create mypackage_test.go
```

## Pull Request Process

### Before Submitting

1. Ensure all tests pass
2. Run linting:
   ```bash
   golangci-lint run
   go fmt ./...
   go vet ./...
   ```
3. Update documentation if needed
4. Add/update tests for your changes

### Pull Request Description

Include:

- Clear title describing the change
- Detailed description of what was changed
- Related issue number (if applicable)
- Testing performed
- Any breaking changes

### Review Process

- PRs require review before merging
- Address reviewer feedback promptly
- Keep PRs focused (one feature/fix per PR)

## Style Guide

### Go Code

- Follow official Go formatting (run `go fmt`)
- Use `gofmt` or `goimports`
- Keep lines under 100 characters when reasonable
- Add comments for exported functions/types
- Use meaningful variable names

### Error Handling

```go
// Good
if err != nil {
    return fmt.Errorf("operation failed: %w", err)
}

// Avoid
_ = someFunction()  // Silent error swallowing
```

### Commit Messages

- Use imperative mood ("Add feature" not "Added feature")
- First line: short summary (50 chars max)
- Body: explain what and why (not how)

```
Add command injection protection in CLI

Previously, user input was passed directly to shell commands.
Now we use separate arguments and validate paths to prevent
execution of arbitrary commands.

Fixes #123
```

## Code of Conduct Examples

### Good

```go
// Comment explaining WHY, not what
// Retry with backoff prevents overwhelming the API during outages
if err := retry(); err != nil {
    return err
}
```

### Avoid

```go
// Do stuff  (unclear)
// Set x to 5  (redundant)
```

## Questions?

Feel free to:

- Open an issue for bugs or feature requests
- Check existing issues before creating new ones
- Join project discussions

## License

By contributing, you agree that your contributions will be licensed under the project's license.
