# DFMC Project — Suggestions for Improvement

> Generated: 2026-05-02 | Language: EN | Profile: generalist

---

## 1. Architecture & Module Design

- **Reduce internal dependency coupling**
  - The `context`, `strings`, `testing`, `fmt`, and `time` modules form tight clusters. Break cycles by extracting shared interfaces early.
  - Prefer Go idioms: keep package depth shallow. If a package needs 3+ levels of subdirs, consider flattening or splitting.

- **Establish a clear module boundary for `tui`**
  - `ui/tui/tui_test.go` is flagged as a hotspot. Ensure UI components are separated from business logic with a well-defined interface layer.
  - Consider a `tui/internal` package for non-exported implementation details.

---

## 2. Code Quality & Maintainability

- **Add unit test coverage for hotspot files**
  - Hotspots: `strings`, `testing`, `fmt`, `time`, `os`, `path/filepath` modules.
  - Write focused tests for edge cases in string manipulation, file path handling, and time formatting.
  - Aim for deterministic, fast tests with no external I/O dependency where possible.

- **Introduce a linting step in CI**
  - Run `go vet`, `staticcheck`, and `golangci-lint` on every PR.
  - This prevents accumulation of technical debt in frequently-changed areas.

- **Document public API surfaces**
  - Every exported function/type in core modules (`context`, `fmt`, `strings`) should have a Go doc comment.
  - Use `go doc` output as a quality gate.

---

## 3. Tooling & Developer Experience

- **Upgrade dependency management**
  - Verify all 673 source files use consistent module paths.
  - Run `go mod tidy` and `go mod verify` in CI.

- **Improve error messages**
  - Use `fmt.Errorf("...: %w", err)` wrapping instead of `errors.New` concatenation for chainable errors.
  - Add contextual hints in errors (e.g., what file or line triggered the issue).

- **Benchmark hot paths**
  - Use the existing `benchmark` tool to profile critical paths in `strings` and `fmt` operations.
  - Establish baseline metrics and add regression alerts.

---

## 4. Testing Strategy

- **Parallelize unit tests**
  - Group independent tests and use `t.Parallel()` where safe.
  - Reduce total test runtime, especially for the `testing` module's own test suite.

- **Add integration tests for TUI components**
  - `ui/tui/tui_test.go` should cover user interaction flows, not just unit-level behavior.
  - Mock terminal I/O to keep tests deterministic.

- **Increase code coverage visibility**
  - Run `go test -cover` in CI and enforce a minimum threshold (e.g., 70%).
  - Publish coverage reports per PR.

---

## 5. Process & Workflow

- **Introduce PR templates**
  - Require: summary of change, affected hotspots, test evidence, and any breaking changes.
  - This improves review quality and ensures context is captured.

- **Adopt semantic commit messages**
  - Use prefixes: `feat:`, `fix:`, `docs:`, `refactor:`, `test:` to make `git log` scannable.

- **Document the project architecture**
  - Keep `architecture.md` and `agents.md` up to date as the codebase evolves.
  - New developers should be able to understand the system from docs + code alone.

---

## Priority Summary

| Priority | Action                                         | Expected Impact |
|----------|-----------------------------------------------|-----------------|
| 🔴 High  | Test coverage for hotspot modules             | Fewer regressions |
| 🔴 High  | CI linting pipeline                           | Cleaner codebase |
| 🟡 Medium | Benchmark hot paths                          | Performance awareness |
| 🟡 Medium | Error wrapping standardization                | Debugging speed |
| 🟢 Low   | PR templates + semantic commits              | Team consistency |

---

*This document is auto-generated. Update it as the project evolves.*
