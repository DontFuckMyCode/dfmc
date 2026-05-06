<!--
Thanks for contributing to DFMC. Keep this PR focused on one change so
review stays fast. Drop any section that doesn't apply, but please leave
the headings so the structure stays consistent across PRs.
-->

## What this changes

<!-- One paragraph: what's different from main, and why. Lead with the
why — "fixes the autonomous-resume race that re-parked on step 1" —
not the how. -->

## How it was tested

<!--
Pick the boxes that apply; remove the rest. Add commands when useful.

- [ ] `go test ./...`
- [ ] `CGO_ENABLED=1 go test -race -count=1 ./...`
- [ ] `go vet ./...` and `gofmt -l .` clean
- [ ] Manual TUI walk: `go run ./cmd/dfmc tui` and exercised the changed surface
- [ ] Manual CLI: `go run ./cmd/dfmc <subcommand> ...`
- [ ] Web cockpit: `go run ./cmd/dfmc serve` and reproduced the change in the browser
-->

## Surfaces touched

<!--
The four layers are kept in sync by convention, not codegen. Tick the
ones this PR has aligned. Most PRs only touch one or two — that's fine
— but a NEW user-facing command should land in all four together.

- [ ] CLI subcommand (`ui/cli/cli_<domain>.go` + dispatch in `cli.go`)
- [ ] TUI slash command (`ui/tui/chat_commands*.go` or `slash_*.go`)
- [ ] Web `/api/v1/...` handler (`ui/web/server_<domain>.go`)
- [ ] Remote client (`ui/cli/cli_remote.go`)
- [ ] Engine subsystem (`internal/engine/engine_*.go` sibling)
- [ ] Tool surface (`internal/tools/...` + spec)
- [ ] Prompt library (`internal/promptlib/defaults/*.yaml`)
- [ ] Docs (`README.md`, `architecture.md`, `agents.md`)
-->

## Risk and rollback

<!--
- What could break? Be specific (existing parked agents, on-disk DB shape, prompt cache layout, ...).
- How to roll back? (`git revert <sha>` is fine for most; call out any data migrations that need a manual step.)
-->

## Notes for review

<!-- Anything that helps the reviewer:
- decisions you weighed and rejected
- test fixtures you added (and why those over alternatives)
- open questions you'd like a second opinion on
-->
