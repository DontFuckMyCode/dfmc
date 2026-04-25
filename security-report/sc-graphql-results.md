# sc-graphql — GraphQL Security Assessment

**Target:** D:\Codebox\PROJECTS\DFMC (Go 1.25)
**Date:** 2026-04-25
**Skill:** sc-graphql (performing-graphql-security-assessment)
**Status:** NOT APPLICABLE — no GraphQL surface exists

## Result

**No issues found by sc-graphql** — no GraphQL libraries in `go.mod` and no `/graphql` route in `ui/web/server.go`'s `setupRoutes`.

## Evidence

### 1. No GraphQL libraries in `go.mod`

Searched `D:\Codebox\PROJECTS\DFMC\go.mod` (case-insensitive) for:
- `graphql`
- `gqlgen` / `99designs/gqlgen`
- `gqlparser` / `vektah/gqlparser`
- `machinebox/graphql`
- `graph-gophers/graphql-go`

Result: **0 matches.** None of the common Go GraphQL stacks are present.

### 2. No `/graphql` route in the web server

Searched `D:\Codebox\PROJECTS\DFMC\ui\web\server.go` (case-insensitive) for `graphql` or `/graphql`.

Result: **0 matches.** The route table in `setupRoutes` exposes only JSON+SSE+WS handlers under `/api/v1/*` (status, chat, context, tools/skills, conversation, workspace, files, drive, task, admin) plus `/ws` SSE and a few static asset paths — consistent with the project's documented architecture (`dfmc serve` is a JSON+SSE+WS REST API).

### 3. Repo-wide `graphql` token sweep

Repo-wide grep (excluding `bin/`, `vendor/`, `node_modules/`, `.dfmc/`, `.git/`, `security-report/`) returned a **single hit**:

- `ui/cli/cli_skills_data.go` lines 196, 202, 205, 206 — appears in the `api` skill's description string and playbook prose (e.g. *"REST, GraphQL, endpoints, schemas, auth"*, *"For GraphQL: name types after the domain, not the implementation."*).

This is **documentation/help text shipped with the binary**, not a server, client, schema, resolver, or import. It does not introduce a GraphQL endpoint and has no runtime behaviour.

## Discovery checks (skipped — no surface)

The following sc-graphql Verification phases were not run because there is no GraphQL endpoint to probe:

- Introspection enabled (`__schema`, `__type`)
- Query depth / recursion limits
- Query batching abuse (array-of-operations)
- Alias-based amplification / cost bypass
- Field suggestion leakage ("Did you mean…")
- Mutation auth gaps
- Subscription auth gaps
- Persisted-query / APQ bypass

If GraphQL is added to DFMC in the future (e.g. an alternate API surface), re-run sc-graphql against the new endpoint before shipping.

## Conclusion

DFMC's external surface is exclusively REST+SSE+WebSocket on `/api/v1/*` and `/ws`. There is no GraphQL parser, no schema, no resolver, no `/graphql` POST handler, and no GraphQL client library compiled into the binary. **sc-graphql is not applicable to this codebase.**
