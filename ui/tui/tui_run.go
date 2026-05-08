package tui

// tui_run.go — bubbletea program lifecycle: Run wires the engine into a
// tea.Program (mouse capture, alt-screen, approver, event subscription)
// and runProgramSafely / runWithPanicGuard restore the terminal on a
// panic so the user doesn't end up staring at a frozen alt-screen.
// Companion siblings:
//
//   - tui.go        Model struct, NewModel, View, Init, projectRoot,
//                   ensureDiagnostics + scroll/mouse constants
//   - tui_types.go  chatRole / coachSeverity / paramStr helpers and the
//                   small data types (chatLine, patchSection, picker
//                   items, suggestion state)

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func Run(ctx context.Context, eng *engine.Engine, opts Options) error {
	if eng == nil {
		return fmt.Errorf("tui engine is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	model := NewModel(ctx, eng)
	model.eventRelayExternal = true
	programOpts := []tea.ProgramOption{}
	// Mouse capture is ON by default — wheel scrolls the transcript, which
	// is what people reach for in a full-screen TUI. Drag-to-select still
	// works via Shift+drag in most terminals, and /mouse flips capture at
	// runtime (or set tui.mouse_capture: false in .dfmc/config.yaml to
	// make the "off" behavior the default).
	if eng.Config != nil {
		if eng.Config.TUI.MouseCapture {
			model.ui.mouseCaptureEnabled = true
			programOpts = append(programOpts, tea.WithMouseCellMotion())
		}
		// Tool strip (per-message tool chips) defaults to expanded (true).
		// Set tui.tool_strip_expanded: false in config to default to collapsed.
		model.ui.toolStripExpanded = eng.Config.TUI.ToolStripExpanded
	}
	if opts.AltScreen {
		programOpts = append(programOpts, tea.WithAltScreen())
	}
	p := tea.NewProgram(model, programOpts...)
	model.program = p

	// Wire the tool-approval gate. SetApprover is a no-op when the engine
	// has tools.require_approval empty, but registering it here is cheap
	// and means flipping the config flag at runtime doesn't need a restart.
	approver := newTeaApprover()
	approver.bindProgram(p)
	eng.SetApprover(approver)
	defer eng.SetApprover(nil)
	unsubscribeEvents := func() {}
	if eng.EventBus != nil {
		unsubscribeEvents = eng.EventBus.SubscribeFunc("*", func(ev engine.Event) {
			p.Send(engineEventMsg{event: ev})
		})
	}
	defer unsubscribeEvents()

	return runProgramSafely(p)
}

// runProgramSafely wraps tea.Program.Run with a panic guard that
// restores the terminal to a usable state on crash. Without this, a
// panic inside any panel's Update/View leaves the terminal stuck in
// alt-screen + mouse-capture + hidden-cursor mode — the user gets a
// blank screen that looks like a hang until they blindly type `reset`.
func runProgramSafely(p *tea.Program) error {
	return runWithPanicGuard(os.Stderr, func() error {
		_, err := p.Run()
		return err
	})
}

// runWithPanicGuard is the testable core: it runs `fn` and, on panic,
// emits ANSI reset sequences to `out`, prints the panic + stack, and
// returns a wrapped error so the caller can exit cleanly. Split out
// from runProgramSafely so tests don't need a real tea.Program.
//
// ANSI sequences emitted on panic:
//   - CSI ?1049l — exit alt screen
//   - CSI ?1000l / ?1002l / ?1006l — disable mouse reporting variants
//   - CSI ?25h — show cursor
//
// Terminals ignore sequences that aren't currently active, so sending
// all of them is safe regardless of which modes were enabled.
func runWithPanicGuard(out io.Writer, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			_, _ = fmt.Fprint(out,
				"\x1b[?1049l\x1b[?1000l\x1b[?1002l\x1b[?1006l\x1b[?25h")
			_, _ = fmt.Fprintf(out, "\nDFMC TUI crashed: %v\n\n", r)
			_, _ = fmt.Fprintf(out, "%s\n", debug.Stack())
			err = fmt.Errorf("tui panic: %v", r)
		}
	}()
	return fn()
}
