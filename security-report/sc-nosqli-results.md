# sc-nosqli Results — DFMC

**Skill:** `sc-nosqli` (NoSQL Injection — MongoDB / Redis / CouchDB / DynamoDB / Elasticsearch)
**Target:** `D:\Codebox\PROJECTS\DFMC` (Go 1.25, module `github.com/dontfuckmycode/dfmc`)
**Date:** 2026-04-25
**Status:** No issues found.

## Summary

**No NoSQL injection surface exists in DFMC.** The project uses only bbolt (`go.etcd.io/bbolt v1.4.3`) as its persistence layer — an embedded single-file key/value store with no query language, no operators, and no remote driver protocol. There is no MongoDB, Redis, CouchDB, DynamoDB, Cassandra, Aerospike, etcd-client, Cosmos, or Elasticsearch client in the dependency graph, and no source-level usage of NoSQL query patterns.

## Evidence

- **`go.mod` direct requires** — only `bbolt` is database-shaped; the rest are TUI (bubbletea, lipgloss), tree-sitter parsers, and stdlib-adjacent (`golang.org/x/{net,sys,time,text}`, `yaml.v3`, `gorilla/websocket`).
- **`go.sum` keyword scan** — `grep -i "mongo|redis|couchbase|dynamodb|cassandra|aerospike|etcd-client|cosmos|elastic"` returned **0 matches** across the full dependency closure.
- **Source-level operator scan** — `grep` for MongoDB operators (`$where`, `$regex`, `$ne`), MongoDB clients (`MongoClient`, `mongoose`), Redis EVAL (`redis.eval`), and Elasticsearch (`query_string`, `painless`) across all `*.go` files returned **0 matches**.
- **Architecture report cross-check** — `security-report/architecture.md` confirms "no SQL, no Redis, no external DB clients"; bbolt is the sole persistence (`internal/storage/store.go`, `internal/memory/store.go`, `internal/conversation`, `internal/taskstore/store.go`, `internal/drive/persistence.go`).
- **bbolt is out-of-scope for sc-nosqli** — it is a B+tree key/value file format with `Bucket.Get(key)` / `Bucket.Put(key, value)` semantics. There is no query language, no operator parsing, no JavaScript execution, no remote protocol — none of the CWE-943 sinks the skill targets exist.

## Discovery Phase Result

Per the SKILL.md activation rule ("Runs when NoSQL databases are detected in the architecture"), the skill has **no applicable target** in this codebase. Discovery terminated after dependency + source negative confirmation; Verification phase not entered.

## Findings

None.
