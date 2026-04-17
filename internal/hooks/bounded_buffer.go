// boundedBuffer is an io.Writer that captures up to `cap` bytes and
// drops the rest, recording a truncation marker. It exists so hook
// stdout/stderr capture can't grow without bound — a misbehaving hook
// (`tail -f`, infinite progress dots, attacker-controlled binary
// piping /dev/urandom) used to OOM the parent because cmd.Output
// reads the whole stream into memory.
//
// Behaviour:
//   - Writes that fit entirely under cap: appended verbatim.
//   - Writes that cross the cap: the head is kept up to cap, the tail
//     dropped, and a single truncation banner appended.
//   - Writes after cap is reached: silently discarded (still report
//     n=len(p) so the writer side keeps streaming and we don't stall
//     the child process on a blocked pipe).
//
// Not safe for concurrent use; cmd.Run guarantees serialised stdout
// and stderr writes per stream.

package hooks

import "fmt"

type boundedBuffer struct {
	cap       int
	data      []byte
	truncated bool
}

func newBoundedBuffer(cap int) *boundedBuffer {
	if cap <= 0 {
		cap = 1
	}
	return &boundedBuffer{cap: cap}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.truncated {
		// Cap already hit; pretend we accepted everything so the child
		// keeps writing and doesn't block on a full pipe. The parent
		// has the head bytes, which is all we need for diagnostics.
		return n, nil
	}
	remaining := b.cap - len(b.data)
	if remaining >= n {
		b.data = append(b.data, p...)
		return n, nil
	}
	// Partial fit: take the head, mark truncated, drop the tail.
	if remaining > 0 {
		b.data = append(b.data, p[:remaining]...)
	}
	b.truncated = true
	return n, nil
}

// String returns the captured content followed by a one-line marker
// when the cap was hit. The marker is bytes appended at render time,
// not while writing, so the captured slice itself still represents
// only what the child actually produced (up to cap).
func (b *boundedBuffer) String() string {
	if !b.truncated {
		return string(b.data)
	}
	return string(b.data) + fmt.Sprintf("\n[hook output truncated at %d bytes]\n", b.cap)
}
