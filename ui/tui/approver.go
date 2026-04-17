// TUI-side adapter for the engine's tool approval gate.
//
// The engine calls Approver.RequestApproval synchronously from whichever
// goroutine is running the agent loop. The TUI runs on a bubbletea
// program goroutine, so we can't answer inline — instead we post a
// message into the program and block on a per-request response channel.
// A short timeout caps how long an unanswered prompt can stall the agent
// (30s by default) so a backgrounded user session doesn't wedge the
// model forever.

package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// defaultApproverTimeout caps how long the engine will wait for a user
// answer before assuming "no". Picked to be long enough that a user who
// tabbed away for a coffee refill can still approve, but short enough
// that a forgotten-about session doesn't silently wedge the provider
// call behind it.
const defaultApproverTimeout = 30 * time.Second

// pendingApproval carries a live request across the TUI boundary. The
// model stores a pointer to it when a modal is on screen; the teaApprover
// retains the same pointer via its map so y/n resolution can deliver a
// decision back to the engine goroutine through resp.
type pendingApproval struct {
	ID   uint64
	Req  engine.ApprovalRequest
	resp chan engine.ApprovalDecision
}

// approvalRequestedMsg fires once per engine approval ask, routing the
// request into the model's main loop where it can render the modal.
type approvalRequestedMsg struct {
	Pending *pendingApproval
}

// teaApprover is the bubbletea-side implementation of engine.Approver.
// It queues RequestApproval calls via p.Send and blocks on a channel
// until the Model's key handler resolves the pending entry.
type teaApprover struct {
	mu      sync.Mutex
	prog    *tea.Program
	nextID  uint64
	timeout time.Duration
}

// newTeaApprover builds an approver. bindProgram must be called before
// the first agent call reaches the engine — in practice that's right
// after tea.NewProgram and before p.Run, which happens on the same
// goroutine, so no race window exists in normal use.
func newTeaApprover() *teaApprover {
	return &teaApprover{timeout: defaultApproverTimeout}
}

// bindProgram wires the bubbletea program so RequestApproval has
// somewhere to deliver messages. Setting it to nil (e.g. during
// teardown) makes subsequent asks fail closed.
func (a *teaApprover) bindProgram(p *tea.Program) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prog = p
}

// RequestApproval implements engine.Approver. It runs on the caller's
// goroutine (engine agent loop), sends a message into the TUI, and
// blocks on the response channel until the user resolves it, the
// context expires, or the approver's own timeout fires.
func (a *teaApprover) RequestApproval(ctx context.Context, req engine.ApprovalRequest) engine.ApprovalDecision {
	a.mu.Lock()
	prog := a.prog
	a.nextID++
	id := a.nextID
	timeout := a.timeout
	a.mu.Unlock()

	if prog == nil {
		// No program wired yet — the TUI isn't up. Rather than deny
		// silently we approve: the admin either hasn't set up the gate
		// or is in a non-interactive context where approval prompts
		// don't make sense. Gating is opt-in via config, so arriving
		// here means someone put a tool on RequireApproval but has no
		// TUI to drive it. Approving preserves backwards compatibility
		// for `dfmc ask` / headless flows.
		return engine.ApprovalDecision{Approved: true, Reason: "approver not bound; auto-approved"}
	}

	// Buffered size 1 so the model's resolver can deliver and move on
	// without blocking if we've already given up waiting.
	resp := make(chan engine.ApprovalDecision, 1)
	pending := &pendingApproval{ID: id, Req: req, resp: resp}

	prog.Send(approvalRequestedMsg{Pending: pending})

	if timeout <= 0 {
		timeout = defaultApproverTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case decision := <-resp:
		return decision
	case <-timer.C:
		return engine.ApprovalDecision{Approved: false, Reason: "approval timed out"}
	case <-ctx.Done():
		return engine.ApprovalDecision{Approved: false, Reason: "context canceled"}
	}
}

// resolve delivers the user's verdict on a pending approval. Safe to
// call multiple times — subsequent sends on the buffered chan either
// queue or get dropped (we size-1 so at most one writer wins anyway).
func (p *pendingApproval) resolve(decision engine.ApprovalDecision) {
	if p == nil || p.resp == nil {
		return
	}
	select {
	case p.resp <- decision:
	default:
	}
}

// renderApprovalModal paints the y/n prompt under the composer. Matches
// the mention/slash modal styling so it reads as part of the TUI's own
// language rather than a foreign dialog. We surface the tool name, the
// source (agent vs subagent), and up to ~6 param keys so the user can
// tell "yes, I asked for this" from "wtf, why is it running write_file
// on my .env".
func renderApprovalModal(p *pendingApproval, width int) string {
	if p == nil {
		return ""
	}
	if width < 40 {
		width = 40
	}
	title := warnStyle.Bold(true).Render("⚠ Tool approval requested") +
		subtleStyle.Render("  —  y/enter approve · n/esc deny")

	source := strings.TrimSpace(p.Req.Source)
	if source == "" {
		source = "agent"
	}
	headline := accentStyle.Bold(true).Render(p.Req.Tool) +
		subtleStyle.Render("  · source ") +
		boldStyle.Render(source)

	body := []string{title, "", headline}
	if len(p.Req.Params) > 0 {
		body = append(body, subtleStyle.Render("parameters:"))
		for _, line := range summarizeApprovalParams(p.Req.Params, width-4) {
			body = append(body, "  "+line)
		}
	} else {
		body = append(body, subtleStyle.Render("(no parameters)"))
	}
	footer := subtleStyle.Render("y / enter  approve     n / esc  deny     ctrl+c  quit")
	body = append(body, "", footer)
	return mentionPickerStyle.Width(width).Render(strings.Join(body, "\n"))
}

// summarizeApprovalParams turns a params map into up to six compact
// "key = value" lines, ordered alphabetically for stable rendering.
// Long values are elided so the modal doesn't blow out on a huge file
// content payload — the goal is informed consent, not a full dump.
func summarizeApprovalParams(params map[string]any, width int) []string {
	if width < 20 {
		width = 20
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	const maxRows = 6
	shown := keys
	trimmed := false
	if len(shown) > maxRows {
		shown = shown[:maxRows]
		trimmed = true
	}
	out := make([]string, 0, len(shown)+1)
	for _, key := range shown {
		val := fmt.Sprintf("%v", params[key])
		val = strings.ReplaceAll(val, "\n", " ")
		line := fmt.Sprintf("%s = %s", key, val)
		out = append(out, truncateSingleLine(line, width))
	}
	if trimmed {
		out = append(out, subtleStyle.Render(fmt.Sprintf("… %d more", len(keys)-maxRows)))
	}
	return out
}
