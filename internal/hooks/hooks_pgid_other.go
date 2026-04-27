//go:build !windows && !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

// Fallback for platforms that lack POSIX process groups AND lack the
// Windows taskkill helper — js/wasm, plan9, etc. dfmc isn't meaningfully
// targeted at these, but the no-op stubs let the package compile so a
// `go build ./...` from a contributor's exotic environment doesn't fail.

package hooks

import "os/exec"

func applyProcessGroupIsolation(_ *exec.Cmd) {}
func killProcessGroup(_ int)                 {}
