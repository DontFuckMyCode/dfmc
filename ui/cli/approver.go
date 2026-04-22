// CLI-side adapter for the engine's tool-approval gate. When the user
// runs `dfmc ask` / `dfmc chat` / `dfmc review` etc. with tools on the
// tools.require_approval list, the agent loop reaches for the engine's
// Approver and we need to prompt the user on stderr/stdin.
//
// Two modes:
//   - Interactive TTY: print the ask to stderr and read y/n from stdin.
//     A blank line defaults to deny - a user who hits enter without
//     looking should not accidentally greenlight write_file.
//   - Non-interactive (stdin redirected, piped, CI, etc.): auto-deny
//     with a reason that tells the user how to opt in. Auto-approving
//     would silently bypass the gate the operator deliberately enabled.

package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/mattn/go-isatty"
	"github.com/muesli/cancelreader"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// stdinApprover reads y/n from the terminal. A sync.Mutex serializes
// concurrent asks from concurrent subagents so overlapping prompts
// do not interleave on stderr.
type stdinApprover struct {
	mu                 sync.Mutex
	reader             *bufio.Reader
	in                 io.Reader
	out                io.Writer
	cancelReader       cancelreader.CancelReader
	isTTY              bool
	autoYes            bool
	autoNo             bool
	autoYesDestructive bool
}

// newStdinApprover builds an approver that respects three env flags for
// headless use:
//   - DFMC_APPROVE=yes             - auto-approve every NON-destructive ask
//     (read_file, list_dir, etc.). Destructive
//     tools still require the second knob below.
//   - DFMC_APPROVE_DESTRUCTIVE=yes - combined with DFMC_APPROVE=yes, also
//     auto-approves destructive tools.
//   - DFMC_APPROVE=no              - auto-deny every ask.
//   - unset + non-TTY stdin        - auto-deny with a reason string.
//   - unset + TTY                  - interactive prompt.
func newStdinApprover() *stdinApprover {
	env := strings.ToLower(strings.TrimSpace(os.Getenv("DFMC_APPROVE")))
	envDestructive := strings.ToLower(strings.TrimSpace(os.Getenv("DFMC_APPROVE_DESTRUCTIVE")))

	readerSrc := io.Reader(os.Stdin)
	var cancelSrc cancelreader.CancelReader
	if cr, err := cancelreader.NewReader(os.Stdin); err == nil {
		cancelSrc = cr
		readerSrc = cr
	}

	return &stdinApprover{
		reader:             bufio.NewReader(readerSrc),
		in:                 readerSrc,
		out:                os.Stderr,
		cancelReader:       cancelSrc,
		isTTY:              isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd()),
		autoYes:            env == "yes" || env == "y" || env == "1" || env == "true",
		autoNo:             env == "no" || env == "n" || env == "0" || env == "false",
		autoYesDestructive: envDestructive == "yes" || envDestructive == "y" || envDestructive == "1" || envDestructive == "true",
	}
}

// RequestApproval implements engine.Approver.
func (a *stdinApprover) RequestApproval(ctx context.Context, req engine.ApprovalRequest) engine.ApprovalDecision {
	if a.autoYes {
		// Two-knob gate: DFMC_APPROVE=yes auto-approves only non-destructive
		// tools. Destructive ones require DFMC_APPROVE_DESTRUCTIVE=yes too.
		if tools.IsDestructive(req.Tool) && !a.autoYesDestructive {
			return engine.ApprovalDecision{
				Approved: false,
				Reason:   "DFMC_APPROVE=yes only auto-approves read-only tools; set DFMC_APPROVE_DESTRUCTIVE=yes to also auto-approve writes/shell",
			}
		}
		return engine.ApprovalDecision{Approved: true, Reason: "DFMC_APPROVE=yes"}
	}
	if a.autoNo {
		return engine.ApprovalDecision{Approved: false, Reason: "DFMC_APPROVE=no"}
	}
	if !a.isTTY {
		return engine.ApprovalDecision{
			Approved: false,
			Reason:   "non-interactive stdin; set DFMC_APPROVE=yes to auto-approve or use the TUI to prompt",
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	fmt.Fprintln(a.out)
	fmt.Fprintln(a.out, "+- DFMC tool approval -----------------------------------------")
	fmt.Fprintf(a.out, "| tool    %s\n", req.Tool)
	fmt.Fprintf(a.out, "| source  %s\n", orDefault(req.Source, "agent"))
	if len(req.Params) > 0 {
		summary := compactJSONParams(req.Params, 240)
		fmt.Fprintf(a.out, "| params  %s\n", summary)
	}
	fmt.Fprintln(a.out, "+--------------------------------------------------------------")
	fmt.Fprintf(a.out, "Approve? [y/N]: ")

	if err := ctx.Err(); err != nil {
		fmt.Fprintln(a.out, "canceled")
		return engine.ApprovalDecision{Approved: false, Reason: "context canceled"}
	}

	line, err := a.readApprovalLine(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(a.out, "canceled")
			return engine.ApprovalDecision{Approved: false, Reason: "context canceled"}
		}
		if err != io.EOF {
			return engine.ApprovalDecision{Approved: false, Reason: fmt.Sprintf("stdin read error: %v", err)}
		}
	}

	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "y" || answer == "yes" {
		return engine.ApprovalDecision{Approved: true}
	}
	return engine.ApprovalDecision{Approved: false, Reason: "user declined"}
}

func (a *stdinApprover) readApprovalLine(ctx context.Context) (string, error) {
	type readResult struct {
		line string
		err  error
	}

	resultCh := make(chan readResult, 1)
	go func() {
		line, err := a.reader.ReadString('\n')
		resultCh <- readResult{line: line, err: err}
	}()

	if a.cancelReader == nil {
		select {
		case <-ctx.Done():
			return "", context.Canceled
		case res := <-resultCh:
			return res.line, res.err
		}
	}

	select {
	case <-ctx.Done():
		a.cancelReader.Cancel()
		res := <-resultCh
		if errors.Is(res.err, cancelreader.ErrCanceled) {
			return "", context.Canceled
		}
		if ctx.Err() != nil {
			return "", context.Canceled
		}
		return res.line, res.err
	case res := <-resultCh:
		if errors.Is(res.err, cancelreader.ErrCanceled) && ctx.Err() != nil {
			return "", context.Canceled
		}
		return res.line, res.err
	}
}

// orDefault returns fallback when s is empty-after-trim.
func orDefault(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// compactJSONParams renders a param map as a one-line JSON blob and
// elides the tail with "..." when it exceeds max. Used only for display;
// arbitrary non-marshallable values fall back to fmt.Sprintf.
func compactJSONParams(params map[string]any, max int) string {
	if max <= 0 {
		max = 240
	}
	b, err := json.Marshal(params)
	if err != nil {
		return fmt.Sprintf("%v", params)
	}
	s := string(b)
	if len(s) <= max {
		return s
	}
	const ellipsis = "..."
	cut := max - len(ellipsis)
	if cut < 0 {
		cut = 0
	}
	return s[:cut] + ellipsis
}
