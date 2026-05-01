# sc-mass-assignment Results

No issues found by sc-mass-assignment.

## Methodology

Audited every body-bearing HTTP/WS handler in `ui/web/` for the
mass-assignment pattern: a JSON decode into a struct that includes
fields the *client* should not be able to set (server-issued IDs,
ownership columns, audit timestamps, role flags, etc.). Verified each
handler either uses a tightly-scoped DTO or rejects/strips the
sensitive fields explicitly.

## Findings

### Server-generated IDs are explicitly rejected

`POST /api/v1/tasks` accepts a `TaskCreateRequest` DTO that *does*
include `ID`, but the handler refuses any client-supplied value with a
400 — `ui/web/server_task.go:109-113`:

```go
// VULN-033: id is server-generated; reject client-supplied values.
if id := strings.TrimSpace(req.ID); id != "" {
    writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id is server-generated; do not supply it in the request body"})
    return
}
id := taskstore.NewTaskID()
```

The handler then constructs `supervisor.Task` field by field with
`strings.TrimSpace` and explicit `append([]string(nil), ...)` slice
copies. There is no `json.Unmarshal` directly into the persisted
`supervisor.Task` shape, so a "smuggle a `CreatedAt`/`Owner`" attack
has nowhere to land.

### `PATCH /api/v1/tasks/{id}` uses an allowlist, not direct unmarshal

`ui/web/server_task.go:178-233` decodes into `map[string]any` and
applies updates field-by-field via an explicit allowlist (`title`,
`detail`, `state`, `summary`, `error`, `confidence`,
`blocked_reason`, `labels`, `parent_id`). Unknown keys are silently
ignored — they cannot reach the persisted record. Plus:

- `state` values are validated against the closed set
  `pending|running|done|blocked|skipped|verifying|waiting|external_review`
  (line 192-201), VULN-032.
- `parent_id` is checked for self-reparent and ancestor cycles
  (line 224-231), VULN-032.

### `POST /api/v1/drive` caps unbounded slice/int fields

`DriveStartRequest` exposes `MaxParallel` and `AutoApprove`. Both are
hard-capped before reaching the driver — `ui/web/server_drive.go:71-84`
(VULN-034). No smuggled fields are unmarshaled into `drive.Run`; the
handler explicitly maps DTO → `drive.Config` field by field.

### Other body-bearing handlers

| Handler | DTO | Risk |
|---|---|---|
| `POST /api/v1/chat` | `ChatRequest{Message}` | one string field |
| `POST /api/v1/ask` | `AskRequest{Message,Race,RaceProviders}` | three explicit fields |
| `POST /api/v1/analyze` | `AnalyzeRequest` (10 explicit scalar fields) | no nested entities |
| `POST /api/v1/conversation/load` | `ConversationLoadRequest{ID}` | string id; load is read-only |
| `POST /api/v1/conversation/branches/create` | `ConversationBranchRequest{Name}` | one string field |
| `POST /api/v1/workspace/apply` | `WorkspaceApplyRequest{Patch,Source,CheckOnly}` | three explicit fields |
| `POST /api/v1/prompts/render` | `PromptRenderRequest` (12 explicit fields) | scalars + string-map vars |
| `POST /api/v1/magicdoc/update` | `MagicDocUpdateRequest` (5 fields) | scalars |
| `POST /api/v1/tools/{name}` | `ToolExecRequest{Params map[string]any}` | params blob is forwarded to tool engine, where each tool validates its own schema (`internal/tools/...`) |
| `POST /api/v1/skills/{name}` | `SkillExecRequest{Input,Message}` | two strings |
| WS `chat`/`ask`/`tool` | local anonymous structs | each handler defines its own narrow struct in `ui/web/server_ws.go:323-413` |

Every handler unmarshal target is a request DTO declared in
`ui/web/server.go:50-123` or in the WS file as a local anonymous
struct — no handler decodes directly into a persistence type.

### Tool params blob is intentional

`ToolExecRequest.Params map[string]any` looks like a mass-assignment
risk but is the correct shape: each tool defines its own JSON schema
in `internal/tools/*` and `tools.Engine.Execute` validates the params
against that schema. The HTTP layer is a transparent forwarder; there
is no "params merging into a struct" surface to attack.

## Verifications

1. Grep for `json.NewDecoder(r.Body).Decode(&req)` across `ui/web/`
   confirmed every target is a request DTO, never a domain type
   (`drive.Run`, `supervisor.Task`, `conversation.Conversation`,
   `engine.Engine`, etc.).
2. Verified that `taskstore.NewTaskID()` is server-side only; checked
   `internal/taskstore/store.go` — no caller path lets a request body
   reach `SaveTask` with the request's own `ID`.
3. Verified that `drive.NewRun(req.Task)` in
   `ui/web/server_drive.go:115` ignores everything in the request
   body except `Task`; `MaxParallel` etc. land in `drive.Config`,
   never on the persisted `*drive.Run`.

## Why this is "no issues, not just N/A"

DFMC's HTTP layer follows the right pattern by convention: every
handler declares a request DTO, unmarshals into it, validates, and
then maps to the persistence type field by field with allowlists.
Server-issued IDs are rejected. Bounded slices/integers are capped.
The two prior CVE-class issues (VULN-032, VULN-033, VULN-034, VULN-042) are
already remediated and pinned by tests in
`ui/web/server_task_integrity_test.go` and `ui/web/server_drive_test.go`.
