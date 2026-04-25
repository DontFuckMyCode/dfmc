# SC: Path Traversal Results

Skill: `sc-path-traversal` (CWE-22, CWE-98)
Target: `D:\Codebox\PROJECTS\DFMC` (Go 1.25)
Date: 2026-04-25

## Summary

DFMC's primary path-traversal defenses (`internal/tools/engine.go` `EnsureWithinRoot`,
`ui/web/server_files.go` `resolvePathWithinRoot`) are well-built — both are
symlink-aware, handle deepest-existing-ancestor for not-yet-created targets, and
reject `..` escapes both lexically and after symlink resolution. Established
file-ops tools (`read_file`, `write_file`, `edit_file`, `apply_patch`,
`list_dir`, `glob`, `find_symbol`, `codemap`, `ast_query`, `test_discovery`) all
route user-controlled paths through `EnsureWithinRoot` correctly.

However, several **newer Phase-7 tools** were added without going through that
helper. They use naive `filepath.Join(projectRoot, userInput)` or even string
concatenation, which `..` segments traverse out of trivially. Combined with the
threat model (the LLM is the attacker; a hostile prompt routed via
`/api/v1/chat` or MCP can issue any tool call inside the agent loop), these are
real exploit paths in the tool layer.

The web `/api/v1/workspace/apply` endpoint also has a TOCTOU/order-of-operations
flaw: its post-apply path-escape check runs AFTER `git apply` has already
written, so it can detect but not prevent escape.

**Total findings: 7** (1 Critical, 3 High, 2 Medium, 1 Low)

---

## Finding: PATH-001

- **Title:** Arbitrary file write via `symbol_move` `to_file` parameter
- **Severity:** Critical
- **Confidence:** 95
- **File:** `internal/tools/symbol_move.go:124-128, 244, 250`
- **Vulnerability Type:** CWE-22 (Path Traversal → Arbitrary File Write)
- **Description:** `symbol_move` accepts a user-controlled `to_file` parameter
  and resolves it as `destPath := filepath.Join(projectRoot, toFile)` with no
  call to `EnsureWithinRoot`. With `to_file="../../tmp/pwned.go"` (or an
  absolute path on Windows / Unix), `filepath.Join` happily produces a path
  that escapes the project root. The tool then:
    1. `os.MkdirAll(destDir, 0755)` — creates directories outside root
    2. `os.WriteFile(destPath, newFileContent, 0644)` — writes attacker-chosen
       Go source content (the moved symbol body, with the model controlling the
       `from`/`to_name` rewrite of the body) outside the project root
  Unlike `edit_file`/`write_file`/`apply_patch`, `symbol_move` has neither
  `EnsureWithinRoot` nor `EnsureReadBeforeMutation` for the destination. The
  read gate only protects sources discovered inside the project tree, not the
  attacker-named destination.
- **Impact:** LLM (or remote actor influencing the LLM via web `/api/v1/chat`
  or MCP prompt) can write arbitrary Go source content to any path the dfmc
  process can reach: home-directory dotfiles, build-tool plugin dirs (
  `~/go/pkg/...`), shell rc files, system temp dirs holding stage payloads, etc.
  Severity is Critical because (a) the write is unprivileged-but-arbitrary
  inside the user's account, (b) `os.MkdirAll` will materialize intermediate
  directories so non-existent ancestors don't block the write, and (c) the
  attacker controls both the path and the content body.
- **Remediation:** Route `toFile` through `EnsureWithinRoot(projectRoot,
  toFile)` before constructing `destPath`. Also wire the `EnsureReadBeforeMutation`
  gate against the destination if the destination already exists (parity with
  `apply_patch`). Reject absolute `to_file` values explicitly with an
  actionable error message.
- **References:** https://cwe.mitre.org/data/definitions/22.html

---

## Finding: PATH-002

- **Title:** Path verification runs after `git apply` writes in `/api/v1/workspace/apply`
- **Severity:** High
- **Confidence:** 90
- **File:** `ui/web/server_workspace.go:217-257` (`applyUnifiedDiffWeb`)
- **Vulnerability Type:** CWE-22 + CWE-367 (TOCTOU after destructive write)
- **Description:** The web workspace-apply handler runs `git apply` (line
  221-223) which performs the actual file modifications. Then, **after** the
  apply has succeeded, lines 233-256 run another `git apply --dry-run
  --porcelain` to enumerate touched paths and verify each resolves inside the
  project root. By that point the writes have already happened. The "deny"
  branch at line 252 returns an error to the HTTP caller but cannot un-apply
  the patch — git has already modified the working tree. Any patch that
  references files outside `root` (e.g. via a `git apply`-honored mechanism
  like a relative path that resolves up via symlink) will write before being
  detected.

  Additionally, the post-check uses
  `strings.HasPrefix(absPath, root)` with **no path-separator boundary**. If
  the project root is `/home/u/proj`, a write to `/home/u/projevil/file`
  passes the prefix check while clearly being outside the intended directory.
  (Narrower than the TOCTOU itself, but compounds it.)
- **Impact:** A hostile patch posted to `/api/v1/workspace/apply` (via a
  prompt-injected LLM, or an authenticated remote API user, or — combined
  with PATH-007 — a CSRF-class scenario where auth=none + bind escape) can
  modify files outside the project root. Even if the post-write check fires
  and returns 400 to the caller, the filesystem damage is already committed.
- **Remediation:**
    1. Run the `--dry-run --porcelain` path enumeration BEFORE the actual
       `git apply` (not after). Reject the request before any write occurs.
    2. Replace the prefix check with `filepath.Rel(root, absPath)` and verify
       the result does not start with `..` + `os.PathSeparator` (the same
       pattern `isPathWithin` in `internal/tools/engine.go:834` uses).
    3. EvalSymlinks `root` once at the top of the function so both sides of
       the comparison are canonicalised the same way.
- **References:** https://cwe.mitre.org/data/definitions/367.html

---

## Finding: PATH-003

- **Title:** Arbitrary file modification via `symbol_rename` `file` parameter
- **Severity:** High
- **Confidence:** 90
- **File:** `internal/tools/symbol_rename.go:124, 188, 198`
- **Vulnerability Type:** CWE-22 (Path Traversal → Arbitrary File Read+Write)
- **Description:** When the user provides a `file` parameter (limiting the
  rename to a single file), the tool builds the target as
  `targetFiles = []string{filepath.Join(projectRoot, file)}` with no
  containment check. With `file="../../some/target.go"`, `filepath.Join`
  produces an out-of-root path. The tool then:
    - calls `findRenameMatches` → `os.ReadFile(filePath)` (line 271) —
      arbitrary file read
    - if `dry_run=false` AND the engine's read-tracker has a matching prior
      read for that abs path, calls `os.WriteFile(fpath, ...)` (line 198) —
      arbitrary file write
  The read-before-mutation gate (line 169) blocks the write path unless a
  matching prior `read_file` exists; since `read_file` itself uses
  `EnsureWithinRoot`, the write is hard to reach. **But the read at line 271
  is not gated and works directly.** With `dry_run=true` (the default code
  path returns `dry_run=false`-default but accepts user override) the tool
  performs only the read; the file's contents bleed into `renameMatch` lines
  (`fullLine`, `lineNum`) which are returned to the model in the `changes`
  slice (line 156).
- **Impact:** Read-only file disclosure of arbitrary host files (whatever the
  dfmc process can read). Write is harder to reach (gated by prior read which
  itself enforces `EnsureWithinRoot`), but the read is enough to leak
  `~/.ssh/config`, `~/.aws/credentials`, `/etc/passwd`, etc.
- **Remediation:** Route `file` through `EnsureWithinRoot(projectRoot, file)`
  at line 123-124 BEFORE the join. Same fix as `find_symbol.go:151`.
- **References:** https://cwe.mitre.org/data/definitions/22.html

---

## Finding: PATH-004

- **Title:** Naive path concatenation in `patch_validation` reads files outside project root
- **Severity:** High
- **Confidence:** 95
- **File:** `internal/tools/patch_validation.go:99, 224`
- **Vulnerability Type:** CWE-22 (Path Traversal → Arbitrary File Read)
- **Description:** Line 99: `abs := projectRoot + "/" + targetPath` and line
  224 (in `ValidatePatchIsClean`): same shape. Both are raw string
  concatenation, NOT `filepath.Join` and certainly not `EnsureWithinRoot`.
  Worse, line 66-68 of `Execute` accepts a user-controlled `project_root`
  override:
  ```go
  if override := strings.TrimSpace(asString(req.Params, "project_root", "")); override != "" {
      projectRoot = override
  }
  ```
  So an attacker can supply both `project_root="/etc"` and
  `patch="--- a/passwd\n+++ b/passwd\n@@ ... @@"` to read `/etc/passwd`
  contents (the file is read at line 100 via `os.ReadFile(abs)`), and the
  bytes are returned to the model via `patched_content_preview` (line 124).

  Also out of scope for path-traversal but worth flagging: `validation_command`
  on line 134-141 runs `exec.CommandContext` with `cmd.Dir = projectRoot`
  (now attacker-controlled via the same override) — bypasses the
  `run_command` allowlist entirely. That's a separate command-execution
  finding for `sc-cmdi`.
- **Impact:** Arbitrary file read of any path the dfmc process can access,
  with contents returned to the model (and potentially echoed to the user
  in conversation). Classic LFI shape.
- **Remediation:**
    1. Drop the `project_root` parameter override entirely, or refuse it
       unless it resolves under the engine's configured project root.
    2. Replace `projectRoot + "/" + targetPath` with
       `EnsureWithinRoot(projectRoot, targetPath)` at both call sites.
    3. Reject patch entries whose target path contains `..` or is absolute
       (parity with `apply_patch.go:86-89`).
- **References:** https://cwe.mitre.org/data/definitions/22.html

---

## Finding: PATH-005

- **Title:** `semantic_search` reads arbitrary files via `file` parameter
- **Severity:** Medium
- **Confidence:** 90
- **File:** `internal/tools/semantic_search.go:149, 202`
- **Vulnerability Type:** CWE-22 (Path Traversal → File Read)
- **Description:** Line 149:
  `targetFiles = []string{filepath.Join(projectRoot, file)}` with no
  `EnsureWithinRoot`. Then `searchFileWithEngine` opens the file via
  `os.ReadFile(fpath)` (line 202), runs the AST engine, and returns matched
  symbols + snippets to the model. With `file="../../etc/hosts"` (or worse,
  any source file outside the project), the tool happily reads it and
  surfaces line content in the `Snippet` and `ContextLines` fields of each
  match (lines 224-256).
- **Impact:** Arbitrary file read with content disclosure (line snippets and
  surrounding context lines) returned to the model. Lower severity than
  PATH-004 because the tool tries to AST-parse the file and only returns
  matches for AST node kinds — but the content is returned literally as
  snippets, so any file with content is leakable.
- **Remediation:** Replace line 149 with
  `abs, err := EnsureWithinRoot(projectRoot, file); if err != nil { return Result{}, err }`
  then `targetFiles = []string{abs}`.
- **References:** https://cwe.mitre.org/data/definitions/22.html

---

## Finding: PATH-006

- **Title:** `disk_usage` walks outside project root via `path` parameter
- **Severity:** Medium
- **Confidence:** 85
- **File:** `internal/tools/disk_usage.go:67-71`
- **Vulnerability Type:** CWE-22 (Path Traversal → Filesystem Enumeration)
- **Description:** Lines 67-71:
  ```go
  if path != "" {
      abs, err := filepath.Abs(filepath.Join(projectRoot, path))
      if err == nil {
          root = abs
      }
  }
  ```
  No `EnsureWithinRoot` containment check. Subsequent `filepath.Walk(root, ...)`
  enumerates everything under that escaped root: total bytes, file counts,
  per-extension breakdown, per-language breakdown, top-10 largest files
  (with full paths via `largest`), and directory summaries. With
  `path="../../../"`, the agent gets a partial map of the host filesystem.
- **Impact:** Filesystem topology disclosure: paths, file sizes, and file
  type distribution outside the project root. No content read, but enough
  to fingerprint the host (e.g. detect a rails app at `~/work/secrets-svc`,
  a 4 GiB DB dump, an SSH key at `~/.ssh/id_rsa` — only the path/size, not
  the bytes). Useful as a recon primitive for further exploitation.
- **Remediation:** Add `EnsureWithinRoot(projectRoot, path)` and use its
  result as `root` instead of the unvalidated `filepath.Abs` form.
- **References:** https://cwe.mitre.org/data/definitions/22.html

---

## Finding: PATH-007

- **Title:** CLI `magicdoc --path <abs>` writes anywhere (low; CLI-only)
- **Severity:** Low
- **Confidence:** 85
- **File:** `ui/cli/cli_magicdoc.go:124-132`
- **Vulnerability Type:** CWE-22 (Path Traversal — locally-trusted boundary)
- **Description:** The CLI's `resolveMagicDocPath` (separate from the web
  version which IS hardened, see `ui/web/server_context.go:462`) accepts an
  absolute `--path` flag and returns it verbatim. `os.WriteFile(target, ...)`
  at line 65 then writes the rendered magic-doc to that absolute path. The
  web version of the same logic was hardened (see
  `TestResolveMagicDocPath_AbsolutePathOutsideRootFallsBack` test case)
  but the CLI version was not.
- **Impact:** A user running `dfmc magicdoc update --path /etc/cron.d/payload`
  would write the rendered magic-doc body there. Since the CLI is the
  locally-trusted entry point in DFMC's threat model (see
  `architecture.md` §5 "Trust Boundaries"), the only realistic abuse path
  is shell-history poisoning or a malicious prompt injection causing the
  user to copy/paste the wrong command. Hence Low.
- **Remediation:** Mirror the web hardening — call `resolvePathWithinRoot`
  (or `EnsureWithinRoot`) and fall back to the default location if the
  caller-supplied path escapes the root. Or, at minimum, refuse absolute
  paths and `..` segments with an explicit error so the user can't
  accidentally `--path /etc/...`.
- **References:** https://cwe.mitre.org/data/definitions/22.html

---

## What was checked and found clean

- **`internal/tools/engine.go::EnsureWithinRoot`** — symlink-aware, walks
  deepest-existing-ancestor for unborn-targets. Solid.
- **`ui/web/server_files.go::resolvePathWithinRoot`** — same shape, also
  solid. Used correctly by `handleFileContent`, `handleScan`,
  `resolveMagicDocPath` (web version), and `handleAnalyze`.
- **`read_file`, `write_file`, `edit_file`, `apply_patch`, `list_dir`,
  `glob`, `find_symbol`, `codemap`, `ast_query`, `test_discovery`** — all
  call `EnsureWithinRoot` on user-supplied paths.
- **`apply_patch`** — has explicit `filepath.Clean` + abs-path-rejection +
  `EnsureWithinRoot` per target (`apply_patch.go:86-92`). Combined with the
  read-before-mutation gate, this is the gold-standard for the tool layer.
- **`/api/v1/files/{path...}`** — fully hardened.
- **`/api/v1/scan?path=`** — gated by `resolvePathWithinRoot`.
- **`/api/v1/magicdoc/update`** — web version uses `resolvePathWithinRoot`
  with fallback to default on escape.
- **Skills/prompts/commands loaders** — read from config-pinned dirs only,
  no runtime user input flows in.
- **MCP server** — no direct file path handling; tools dispatch through
  the same gates as the agent loop.
- **`internal/config/config.go`** — `FindProjectRoot` walks up from `cwd`
  for `.dfmc/`, no user-controlled path injection.
- **Embedded `//go:embed` static** — compiled in, no runtime path
  resolution needed.

## Common false positives ruled out

- `gh_pr.go` shells out to `gh` with no path arguments user-supplied.
- `git_worktree.go` paths are routed through `rejectGitFlagInjection`.
- `find_symbol.go::path` and `codemap.go::root` correctly call
  `EnsureWithinRoot` (lines 151 and 155 respectively).
- `web_fetch`/`web_search` are URL-based, not filesystem-based.

## Recommendations (prioritized)

1. **PATH-001** (Critical) — fix `symbol_move` immediately; it's a
   straight-up arbitrary file write.
2. **PATH-002** (High) — reorder the `applyUnifiedDiffWeb` checks so
   verification happens before `git apply`, and replace the prefix check
   with `filepath.Rel`-based containment.
3. **PATH-003, PATH-004** (High) — the symbol_rename and patch_validation
   fixes are one-line each (`EnsureWithinRoot`).
4. **PATH-005, PATH-006** (Medium) — add `EnsureWithinRoot` to
   `semantic_search` and `disk_usage`.
5. **PATH-007** (Low) — port the web-version magicdoc hardening to the CLI.

A repo-wide grep for `filepath\.Join\(.*[Pp]rojectRoot,` followed by a
quick triage for `EnsureWithinRoot` in the same function would catch
similar oversights as new tools land. Consider adding a CI lint or
`go vet`-style check for "path tool that takes user input but doesn't
call EnsureWithinRoot in the same call frame."
