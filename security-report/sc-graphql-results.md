# sc-graphql — GraphQL Security Assessment

**Date:** 2026-04-29
**Scope:** D:\Codebox\PROJECTS\DFMC
**Status:** NOT APPLICABLE — no GraphQL surface exists

## Verdict

No findings. DFMC has no GraphQL server, no GraphQL client, no schema, no resolver, and no `/graphql` route. The mentions of "GraphQL" in the source tree are documentation strings shipped with the binary.

## Verification

### 1. `go.mod` — no GraphQL libraries

```
Pattern: graphql|gqlgen|graph-gophers|machinebox
Result:  0 matches in go.mod
```

None of the standard Go GraphQL libraries are declared:
- `github.com/99designs/gqlgen`
- `github.com/graphql-go/graphql`
- `github.com/graph-gophers/graphql-go`
- `github.com/machinebox/graphql`
- `github.com/Khan/genqlient`

### 2. No `/graphql` route in the web server

The HTTP route table in [ui/web/server.go:300-396](../ui/web/server.go) (the `setupRoutes` block) registers 50+ routes under `/api/v1/*`, plus `/`, `/healthz`, `/ws`. None match `/graphql` (case-insensitive) or any `Query{` / `Mutation{` / `Subscription{` schema marker.

### 3. Repo-wide `graphql` token sweep

```
Pattern: graphql (case-insensitive)
Hits in production code:
- ui/cli/cli_skills_data.go:196,202,205,206 — appears in the `api` skill's
  description text and playbook prose ("REST, GraphQL, endpoints, schemas,
  auth", "For GraphQL: name types after the domain, not the implementation").
```

These are documentation strings displayed to the user when they invoke the `api` skill shortcut. They contain no parser, no schema, no executor, and no runtime behaviour.

### 4. WS / SSE channels are JSON-RPC 2.0, not GraphQL subscriptions

`/api/v1/ws` is documented as "WebSocket JSON-RPC 2.0" in [security-report/architecture.md:134](architecture.md). Methods: `chat`, `ask`, `tool`, `drive_start`, `drive_stop`, `drive_status`, `events_subscribe`, `events_unsubscribe`. No `subscription` / `query` / `mutation` envelope.

## Phases not run

The following sc-graphql verification phases were skipped because there is no GraphQL endpoint to probe:
- Introspection check (`__schema`, `__type` queries)
- Field-suggestion leakage
- Query-depth / complexity DoS
- Aliased-batched query DoS
- Authorisation per-resolver
- CSRF on POST /graphql
- WebSocket subscription authentication (graphql-ws / graphql-transport-ws)

## Bottom line

DFMC's external surface is exclusively REST+SSE+WebSocket on `/api/v1/*` and `/ws`. There is no GraphQL parser, no schema, no resolver, no `/graphql` POST handler, and no GraphQL client library in the binary. **sc-graphql is not applicable to this codebase.** Re-run if a GraphQL surface is added in the future.
