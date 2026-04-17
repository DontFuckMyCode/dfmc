// boundedBuffer caps captured tool output at a fixed byte budget so a
// runaway subprocess (build with --verbose, log dump, accidental
// `cat /dev/urandom`) can't drive DFMC's parent heap up without
// bound. Used by run_command for stdout + stderr capture.
//
// Same shape as the one in internal/hooks; kept local because tools
// has no dependency on hooks and the cap value is different (build
// outputs are bigger than hook chatter, so 4 MiB here vs 1 MiB
// there). The duplication is one struct and 25 lines — cheaper than
// dragging both packages onto a shared util import.

package tools

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
		// Past the cap. Pretend we accepted the bytes so the producer
		// keeps streaming and doesn't block on a full pipe — the head
		// is what we wanted anyway.
		return n, nil
	}
	remaining := b.cap - len(b.data)
	if remaining >= n {
		b.data = append(b.data, p...)
		return n, nil
	}
	if remaining > 0 {
		b.data = append(b.data, p[:remaining]...)
	}
	b.truncated = true
	return n, nil
}

func (b *boundedBuffer) String() string {
	if !b.truncated {
		return string(b.data)
	}
	return string(b.data) + fmt.Sprintf("\n[output truncated at %d bytes]\n", b.cap)
}

func (b *boundedBuffer) Len() int {
	return len(b.data)
}
