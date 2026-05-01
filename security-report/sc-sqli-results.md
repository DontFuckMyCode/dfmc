# sc-sqli — SQL Injection

**Date:** 2026-04-29
**Scope:** D:\Codebox\PROJECTS\DFMC
**Status:** NOT APPLICABLE — no SQL anywhere in the codebase

## Verdict

No findings. DFMC has no SQL surface to inject against. The only persistence layer is bbolt (key-value), which has no query language.

## Verification

### 1. `go.mod` — no SQL drivers

[D:\Codebox\PROJECTS\DFMC\go.mod](../go.mod) was read in full (43 lines). Direct dependencies:

- `github.com/charmbracelet/*` (TUI)
- `github.com/gorilla/websocket`
- `github.com/tetratelabs/wazero` (WASM sandbox)
- `github.com/tree-sitter/*` (AST grammars)
- `go.etcd.io/bbolt` (KV store, **not SQL**)
- `golang.org/x/{net,sys,time}`
- `gopkg.in/yaml.v3`

None of `github.com/jackc/pgx`, `github.com/lib/pq`, `github.com/go-sql-driver/mysql`, `github.com/mattn/go-sqlite3`, `modernc.org/sqlite`, `github.com/denisenkom/go-mssqldb`, `github.com/jmoiron/sqlx`, `github.com/uptrace/bun`, `gorm.io/gorm`, `entgo.io/ent`, or any other SQL driver / ORM is declared.

### 2. Source-code grep for SQL imports

```
Pattern: database/sql|github.com/jackc/pgx|github.com/lib/pq|go-sql-driver/mysql|mattn/go-sqlite3|modernc.org/sqlite
Result:  0 matches in *.go files
```

No file in the repository imports `database/sql` or any third-party SQL driver.

### 3. Source-code grep for query-shaped strings

`SELECT `, `INSERT INTO `, `UPDATE `, `DELETE FROM ` only appear inside:

- `internal/security/*.go` rule definitions (DFMC's own scanner) — these are **detection patterns shipped to audit other projects**, not queries DFMC itself executes.
- `internal/langintel/go_kb.go` knowledge-base prose for the `go-sql-injection` lint hint shown to the LLM.
- `ui/cli/cli_skills_data.go` skill description text.

None of these are concatenations into a runtime query.

### 4. Storage layer

`internal/storage/store.go` opens a single bbolt file per project (`go.etcd.io/bbolt`). All access goes through `tx.Bucket([]byte(<fixed name>)).Put / Get / Delete` with byte slices. There is no query language, no string concatenation into a query, and no SQL driver in the dependency closure (verified against indirect imports as well).

## Bottom line

sc-sqli is not applicable to DFMC. There is no SQL parser, driver, ORM, or query string anywhere in the production path. If a future change introduces `database/sql` or any third-party SQL client, re-run this scan; until then the verdict is permanent.
