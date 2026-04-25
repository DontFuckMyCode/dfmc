# sc-sqli — SQL Injection Findings

**Target**: DFMC (`D:\Codebox\PROJECTS\DFMC`)
**Skill**: sc-sqli
**CWE**: CWE-89 (Improper Neutralization of Special Elements used in an
SQL Command).

---

## Counts

| Severity | Count |
|---|---|
| High     | 0 |
| Medium   | 0 |
| Low      | 0 |
| Info     | 1 |
| **Total**| **1** |

---

## SQLI-001 — No SQL execution surface in DFMC

- **File:line**: n/a (negative finding)
- **Severity**: Info
- **Confidence**: H
- **CWE**: CWE-89

DFMC has **no SQL** anywhere. Confirmed by:

### 1. Architecture report

Per `security-report/architecture.md` §1:

> **Storage**: `go.etcd.io/bbolt v1.4.3` (single-process embedded KV)
> ...
> No SQL, no Redis, no external DB clients.

### 2. Repo-wide grep evidence

A search for the standard Go SQL packages and popular ORMs / drivers
returned **zero matches** outside string literals in security-rule
descriptions:

| Pattern | Result |
|---|---|
| `database/sql` | not imported anywhere |
| `gorm.io/gorm`, `jinzhu/gorm` | not imported |
| `github.com/jmoiron/sqlx` | not imported |
| `github.com/lib/pq` | not imported |
| `github.com/jackc/pgx` | not imported |
| `github.com/mattn/go-sqlite3` / `modernc.org/sqlite` | not imported |
| `github.com/go-sql-driver/mysql` | not imported |

The single match for the substring `go-sql-injection` is at
`internal/langintel/go_kb.go:206` — this is a string ID for a
*scanner rule* the langintel knowledge base advertises to the LLM
("Never interpolate user input into SQL strings — use parameterized
queries"). It is metadata DFMC's scanner surfaces about *target*
codebases, not SQL DFMC executes itself.

### 3. Persistence model

The only persistence mechanisms in DFMC are:

- **bbolt** (`internal/storage/store.go`) — embedded key/value store,
  no query language.
- **JSONL files** for conversations (`internal/conversation/manager.go`).
- **Plain files** for `.dfmc/knowledge.json`, `conventions.json`,
  `magic/MAGIC_DOC.md`, prompt overlays, hooks config.
- **Outbound HTTP** to LLM providers (which return JSON, not SQL).

There is no SQL execution surface. Provider responses occasionally
contain SQL strings as *content* (e.g., when the user is debugging an
SQL query and the LLM returns one), but these strings flow into either
chat output (rendered as `<pre>` text) or `read_file` / `edit_file`
parameters (treated as opaque file content) — never into a query
executor.

### Edge case checks

- **Search-style endpoints**: `GET /api/v1/conversations/search` and
  similar — implemented as in-memory filters over JSON-decoded
  conversation records, not SQL.
- **`grep_codebase` tool**: pattern arg is a regex shelled to
  ripgrep when available, otherwise a Go `regexp.Regexp`. Neither is
  SQL.
- **Codemap queries**: `internal/codemap/` operates on AST graph
  structures in memory; no query language.
- **Task store filters**: `internal/taskstore/store.go` `ListTasks`
  walks the bucket with `b.ForEach` and applies Go-side filter
  predicates — no SQL.

---

## Verdict

**No SQL injection is possible** because there is no SQL. If a future
release introduces a SQL backend (the architecture suggests this is
unlikely — bbolt is deliberately embedded for the single-process
locking guarantee), this finding should be revisited.

For NoSQL-style key handling concerns specific to bbolt (bucket
namespacing, key DoS, control-byte keys), see `sc-nosqli-results.md`.
