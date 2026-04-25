# sc-deserialization Results — DFMC

**Target:** `D:\Codebox\PROJECTS\DFMC` (Go 1.25, single-binary code intelligence assistant)
**Skill:** sc-deserialization (CWE-502)
**Date:** 2026-04-25

---

## Executive Summary

**No issues found by sc-deserialization.**

DFMC's Go source contains **no calls to dangerous-class deserialization sinks**. The only serialization formats present are:

| Format | Risk class (per skill SAFE/UNSAFE table) | Where |
|---|---|---|
| `encoding/json` | Safe — data only, no arbitrary type instantiation | All HTTP bodies, MCP JSON-RPC frames, all bbolt-persisted records, LLM provider wire format |
| `gopkg.in/yaml.v3` | Safe — Go's static typing prevents the pyyaml-`!!python/object` class; v3 has no equivalent of v2's deprecated unsafe behaviours | Config files, prompt-library frontmatter, skill manifests, plugin manifests |

**No** `encoding/gob`, **no** `encoding/xml` decoder, **no** CBOR / MessagePack / Protobuf-Any / `gopkg.in/yaml.v2` (whose `Unmarshal` was likewise data-only but is the older API). No custom `UnmarshalJSON` methods anywhere in the tree (verified across `internal/` and `ui/`).

---

## Discovery

### Phase 1 — keyword sweep

Patterns searched across the entire tree (excluding `bin/`, `vendor/`, `node_modules/`, `.dfmc/`, `.git/`, `security-report/`):

| Pattern | Hits |
|---|---|
| `encoding/gob`, `gob.NewDecoder`, `gob.Decode` | **0** |
| `xml.Unmarshal`, `xml.NewDecoder`, `cbor.`, `msgpack.` | **0** |
| `UnmarshalJSON` (custom side-effecting decoder) | **0** files |
| `json.Unmarshal` into `interface{}` / `&map[string]interface{}` (pattern `json\.Unmarshal\(.*interface\{\}`) | **0** |
| `yaml.v2` import (the older API) | **0** — only `gopkg.in/yaml.v3 v3.0.1` is in `go.mod:21` |
| `yaml.Unmarshal` calls | 25 (all reviewed below) |
| `json.NewDecoder(r.Body).Decode` (HTTP body sinks) | 14 (all decode into typed request structs; not a deserialization-vuln class for `encoding/json`) |

### Phase 1 — focus areas from the user prompt

#### `encoding/gob`
**Not present.** No file in the repository imports `encoding/gob`. Confirmed via grep.

#### bbolt-persisted JSON payloads (memory tiers, conversations, drive runs, task store, supervisor state)

Verified call sites — every one decodes into a **typed struct** with `encoding/json`:

| File:line | Sink |
|---|---|
| `internal/memory/store.go:143` | `json.Unmarshal(v, &e)` where `e memory.Entry` |
| `internal/conversation/manager.go` | `json.Unmarshal` into `Message` / `Conversation` (typed) — no `interface{}` decode |
| `internal/drive/persistence.go:96, 114` | `json.Unmarshal(data, &run)` where `run drive.Run` |
| `internal/taskstore/store.go:68, 122, 180` | `json.Unmarshal(v, &t)` where `t supervisor.Task` |
| `internal/supervisor/persistence.go:49, 71` | `json.Unmarshal` into typed handoff/coordinator structs |

`encoding/json` decoding into typed Go structs is the SAFE entry in the skill's risk table — no arbitrary type instantiation, no code execution capability. No `UnmarshalJSON` method anywhere in the tree means there are no side-effecting decoders that could be tricked into doing more than field assignment.

The threat the user prompt called out — *"if the AI loads a supplied memory snapshot or imports a conversation from another source, that's a vector"* — would require **either** (a) a code-execution class deserializer (none present), or (b) a custom `UnmarshalJSON` with side effects (none present). What an attacker *could* do with a malicious bbolt file is **out of scope for sc-deserialization** (it would be bbolt-corruption / business-logic, not CWE-502).

#### MCP JSON-RPC over stdio (`internal/mcp/`)

Untrusted input (per architecture.md §6: "external MCP servers ... untrusted-by-default") flows through:

- `internal/mcp/server.go:38` — `json.NewDecoder(bufio.NewReader(s.in))`
- `internal/mcp/server.go:59` — `json.Unmarshal(raw, &req)` into `jsonRPCRequest` (typed)
- `internal/mcp/server.go:111, 136` — `json.Unmarshal(req.Params, &params)` into typed param structs per method
- `internal/mcp/client.go:210, 258` — `json.Unmarshal(raw, resp)` and `json.Unmarshal(b, target)` into typed response structs

All decodes target concrete Go structs. No `interface{}` sinks, no custom `UnmarshalJSON`. JSON parsed by `encoding/json` cannot instantiate arbitrary types. **Not a CWE-502 finding.**

#### HTTP request bodies (`ui/web/server*.go`)

14 `json.NewDecoder(r.Body).Decode(&req)` sites across `server_chat.go`, `server_drive.go`, `server_conversation.go`, `server_context.go`, `server_tools_skills.go`, `server_workspace.go`, `server_task.go`. Each decodes into a per-handler typed struct, body capped at 4 MiB by `MaxBytesReader` middleware (architecture.md §5). Same conclusion: data-only `encoding/json`, no CWE-502 class.

#### YAML (`gopkg.in/yaml.v3`)

25 `yaml.Unmarshal` call sites. Reviewed:

- **Config files** (`internal/config/config.go:109`, `ui/cli/cli_config.go:317,360,378,401`) — read from `~/.dfmc/config.yaml` / `<project>/.dfmc/config.yaml`, both **local-trusted** paths (architecture §5: project state, gitignored, owned by the invoking user).
- **Prompt library** (`internal/promptlib/promptlib.go:497,501,549`) — embedded defaults plus user-owned overlay paths.
- **Skill / plugin manifests** (`internal/skills/catalog.go:345,421,428,456`, `ui/cli/cli_skill.go:238`, `ui/cli/cli_plugin_install.go:359`) — local manifest files.
- **TUI provider panels** (`ui/tui/provider_panel*.go`, `ui/tui/provider_selection.go`) — read the same local config.
- **Web round-trip** (`ui/web/server_admin.go:211`) — round-trip of data the server itself just marshalled (fully trusted self-source).
- **Tests** (`internal/engine/agent_compact_test.go`, `ui/tui/tui_test.go`) — fixture data.

Per the skill risk table, **YAML is "Dangerous" only for parsers that instantiate language objects** (Python `yaml.load` with FullLoader, SnakeYAML default constructor, Ruby `YAML.load`). `gopkg.in/yaml.v3` decodes into Go structs / maps / slices — Go has no runtime type-injection facility analogous to pyyaml's `!!python/object` or SnakeYAML's `!!javax.script.ScriptEngineManager`. The user prompt explicitly acknowledged this ("yaml.v3 doesn't have the !!python/object class issue").

Remaining yaml.v3-specific concerns the prompt flagged:

- **Arbitrary-tag handling**: yaml.v3 ignores unknown explicit tags (e.g. `!!foo`) when decoding into typed targets — they produce a decode error, not type instantiation. No code path uses a `yaml.Node` with custom `Decode` resolvers that re-interpret tags.
- **Nested-key bombs / billion-laughs**: yaml.v3 implements alias-expansion limits internally (alias-depth and total-byte caps). Even if those were absent, the input sources are local config files **the user already owns**, capped in practical size — not a network-reachable surface. Threat model excludes a hostile local config because such an attacker already has full process-level access.

No YAML decode site sources its input from an HTTP body, an MCP frame, or any other network channel.

### Phase 2 — verification

For every potentially-suspicious path, the chain `untrusted-input → decoder` was traced end-to-end:

1. **HTTP → JSON struct decode**: confirmed body cap (`server.go:314`), confirmed typed target, no custom `UnmarshalJSON`. Safe.
2. **MCP stdio → JSON struct decode**: confirmed typed target on every method dispatch. Safe.
3. **bbolt → JSON struct decode**: local file source, typed target, no custom decoder. Safe.
4. **Config file → YAML struct decode**: local trusted path, yaml.v3 with typed target. Safe.

No path satisfies the skill's exploitability test:
- Q1 *"untrusted source?"* — only HTTP/MCP qualify; YAML/bbolt are local-trusted.
- Q2 *"known gadget chains in classpath?"* — N/A; Go has no gadget-chain ecosystem.
- Q3 *"format capable of arbitrary object instantiation?"* — `encoding/json` and `gopkg.in/yaml.v3` are not.
- Q4 *"type restrictions / allowlisting?"* — every decode targets a named struct; the language enforces this for free.

---

## Findings

**None.**

No issues found by sc-deserialization.

---

## False-positive log (intentionally not raised)

| Pattern | Why not flagged |
|---|---|
| `yaml.Unmarshal(data, &out)` where `out map[string]any` (`server_admin.go:211`) | yaml.v3 decoding to `map[string]any` produces only Go primitives / maps / slices — no language-level object instantiation. Source is a self-marshalled value. Not CWE-502. |
| `json.NewDecoder(r.Body).Decode(&req)` across web handlers | `encoding/json` is data-only per the skill SAFE table. No custom `UnmarshalJSON` methods exist in the tree to add side effects. |
| bbolt-stored JSON in memory/conversations/drive/task/supervisor | Same as above — typed `encoding/json` decode. Local file vector is out of scope for CWE-502 (would be a different class entirely). |
| MCP JSON-RPC frames from external servers | `encoding/json` into typed JSON-RPC structs. The injection vectors against MCP are tool-description spoofing and prompt poisoning (covered by separate skills), not deserialization. |

---

## Notes for downstream skills

- **MCP tool-output poisoning** (hostile MCP server returning crafted strings the agent reads) is a real concern flagged in `architecture.md §6` but belongs to `sc-prompt-injection` / `sc-business-logic`, not deserialization.
- **bbolt file tampering** (a malicious memory/conversation snapshot dropped into `.dfmc/`) is local-attacker-already-has-fs-access territory — not a remote-deserialization class.
- The **absence of `UnmarshalJSON` customisation** repo-wide is genuinely the strongest signal here: even if a future contributor adds one with side effects, that single addition is what would re-open this skill's risk surface. Worth a CI grep guard if the project wants belt-and-braces.
