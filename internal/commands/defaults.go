package commands

import (
	"fmt"
	"os"
	"sync"
)

// defaults.go — DefaultRegistry construction and the sync.Once-cached
// shared instance. The shipped command manifest itself lives in
// defaults_catalog.go (defaultCommands() returns the full Command slice).
//
// Conventions for catalog entries:
//   * Summary is one sentence, <=72 chars, starts with a verb.
//   * Description is multi-line prose; omit for trivially obvious commands.
//   * Usage lists the argument shape without re-stating the name.
//   * Aliases never include the canonical name; Register() rejects dupes.

// DefaultRegistry returns the process-wide shared Registry pre-populated
// with the complete shipped command catalog. A duplicate name or alias
// collision in the catalog is a programmer bug — but it must not kill the
// binary. The degraded-startup commands in main.go (help, version, doctor,
// completion, man) need to keep running even when something is wrong with
// the catalog so the operator can diagnose. We log loudly to stderr and
// skip the offending entry; CI catches this via
// TestDefaultRegistry_BootsWithoutPanic + TestDefaultRegistry_NoDupes.
//
// The Registry is built once via sync.Once and shared across callers.
// Pre-cache, every HTTP request to /api/v1/commands, every slash-menu
// keystroke, every TUI help refresh, and every CLI suggest call rebuilt
// the catalog (~30 Register calls + the same number of map allocations).
// Sharing is safe because the Registry is read-only after construction:
// Lookup / All / ForSurface / ListByCategory / Names / RenderHelp /
// RenderCommandHelp only READ from the underlying maps, and Go's memory
// model guarantees concurrent reads of a map that's never written are
// race-free. NewRegistry remains the right entry point for tests that
// need an isolated, mutable Registry — they should not call this.
//
// If a future change needs to mutate the registry at runtime (dynamic
// plugin commands, hot reload, etc.) the cache must be invalidated and
// the read paths need a sync.RWMutex; do that work AT the change, not
// pre-emptively.
func DefaultRegistry() *Registry {
	defaultRegistryOnce.Do(func() {
		r := NewRegistry()
		for _, cmd := range defaultCommands() {
			if err := r.Register(cmd); err != nil {
				fmt.Fprintf(os.Stderr, "dfmc: command catalog: skipping %q: %v\n", cmd.Name, err)
			}
		}
		defaultRegistry = r
	})
	return defaultRegistry
}

var (
	defaultRegistryOnce sync.Once
	defaultRegistry     *Registry
)
