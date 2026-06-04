package cli

import (
	"strings"
	"testing"
)

// cliRemoteParity is the documented cross-layer contract between the
// `dfmc <cmd>` CLI surface and the `dfmc remote <cmd>` client surface.
//
// CLAUDE.md notes the four command layers (CLI / web / remote / MCP) are
// "kept in sync by convention, not by codegen". TestCommandNamesDerived*
// already guard each layer's INTERNAL derivation (help/completion vs
// dispatcher; remote usage vs registry). What was missing is a
// CROSS-layer tripwire: when a contributor adds a CLI command they get
// no signal to consider the remote client, and vice versa.
//
// This map is the single source of truth. The value is true when a
// `dfmc remote <same-name>` subcommand exists. It is a mechanical
// same-name proxy, NOT "has any remote capability": e.g. CLI `map` is
// reachable remotely as `remote codemap`, so its names differ and it is
// recorded false here. Local-only commands (init, serve, tui, config,
// doctor, update, …) are legitimately remote:false.
//
// Adding/removing a CLI command, or adding a same-name remote client,
// fails the test below and forces a conscious update of this contract —
// converting "sync by convention" into a mechanical guarantee for the
// CLI<->remote overlap.
var cliRemoteParity = map[string]bool{
	// Conversational / analysis commands with a remote client.
	"ask":          true,
	"tool":         true,
	"skill":        true,
	"agents":       true,
	"prompt":       true,
	"magicdoc":     true,
	"analyze":      true,
	"context":      true,
	"memory":       true,
	"conversation": true,
	"drive":        true,
	"status":       true,

	// Local-only commands: no same-name `dfmc remote` subcommand.
	"help":        false,
	"version":     false,
	"init":        false,
	"chat":        false, // interactive REPL; remote uses `remote ask`
	"tui":         false,
	"map":         false, // remotely reachable as `remote codemap`
	"scan":        false,
	"conv":        false, // alias of conversation; remote uses full name
	"serve":       false,
	"config":      false,
	"plugin":      false,
	"agent":       false, // alias of agents; remote uses `remote agents`
	"remote":      false,
	"provider":    false,
	"model":       false,
	"providers":   false,
	"doctor":      false,
	"hooks":       false,
	"approvals":   false,
	"approve":     false,
	"permissions": false,
	"mcp":         false,
	"update":      false,
	"completion":  false,
	"man":         false,
}

// TestCLIRemoteParityContractCoversEveryCommand is the tripwire: the
// contract map must list exactly the CLI dispatcher's commands (minus
// flag aliases like -h/--help). A new CLI command fails here until the
// author records its remote-parity status.
func TestCLIRemoteParityContractCoversEveryCommand(t *testing.T) {
	registry := commandHandlerRegistry()

	// Every non-flag dispatcher command must be in the contract.
	for name := range registry {
		if strings.HasPrefix(name, "-") {
			continue // flag alias (-h, --help) — not a command
		}
		if _, ok := cliRemoteParity[name]; !ok {
			t.Fatalf("CLI command %q is not in cliRemoteParity — add it and declare whether `dfmc remote %s` should exist", name, name)
		}
	}

	// The contract must not carry stale entries the dispatcher dropped.
	for name := range cliRemoteParity {
		if _, ok := registry[name]; !ok {
			t.Fatalf("cliRemoteParity lists %q but the CLI dispatcher no longer has it — remove the stale contract entry", name)
		}
	}
}

// TestCLIRemoteParityMatchesRemoteRegistry enforces the cross-layer
// guarantee in both directions against the live remote registry, so the
// contract can't silently drift from reality.
func TestCLIRemoteParityMatchesRemoteRegistry(t *testing.T) {
	remote := remoteCommandRegistry()

	for cmd, wantRemote := range cliRemoteParity {
		_, hasRemote := remote[cmd]
		switch {
		case wantRemote && !hasRemote:
			t.Fatalf("contract says CLI %q has a `dfmc remote %s` client, but remoteCommandRegistry has no such command — wire it or flip the contract to false", cmd, cmd)
		case !wantRemote && hasRemote:
			t.Fatalf("a `dfmc remote %s` client now exists but the CLI<->remote contract marks %q remote:false — flip it to true so the parity is acknowledged", cmd, cmd)
		}
	}
}
