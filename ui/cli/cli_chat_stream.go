package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func streamAskToStdout(ctx context.Context, eng *engine.Engine, prompt, errorPrefix string) bool {
	stream, err := eng.StreamAsk(ctx, prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", errorPrefix, err)
		return false
	}
	printProviderStream(stream)
	return true
}

func printProviderStream(stream <-chan provider.StreamEvent) {
	printed := false
	endsWithNL := true
	for ev := range stream {
		switch ev.Type {
		case "delta":
			fmt.Print(ev.Delta)
			printed = true
			endsWithNL = strings.HasSuffix(ev.Delta, "\n")
		case "error":
			if printed && !endsWithNL {
				fmt.Println()
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", ev.Err)
			printed = false
		}
	}
	if printed && !endsWithNL {
		fmt.Println()
	}
}

func handleSlashRetry(ctx context.Context, eng *engine.Engine) {
	active := eng.ConversationActive()
	if active == nil {
		fmt.Fprintln(os.Stderr, "no active conversation")
		return
	}
	msgs := active.Messages()
	var lastUser string
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == types.RoleUser {
			lastUser = msgs[i].Content
			break
		}
	}
	if lastUser == "" {
		fmt.Fprintln(os.Stderr, "no prior user message to retry")
		return
	}
	_, _ = eng.ConversationUndoLast()
	fmt.Println("(resending last question...)")
	streamAskToStdout(ctx, eng, lastUser, "retry error")
}

func handleSlashContinue(ctx context.Context, eng *engine.Engine, args []string) {
	if !eng.HasParkedAgent() {
		fmt.Fprintln(os.Stderr, "nothing to resume - no parked agent loop")
		return
	}
	note := strings.TrimSpace(strings.Join(args, " "))
	fmt.Println("(resuming agent loop...)")
	streamAskToStdout(ctx, eng, note, "resume error")
}

func handleSlashAsk(ctx context.Context, eng *engine.Engine, args []string) {
	payload := strings.TrimSpace(strings.Join(args, " "))
	if payload == "" {
		fmt.Fprintln(os.Stderr, "usage: /ask <question>")
		return
	}
	streamAskToStdout(ctx, eng, payload, "ask error")
}

func handleSlashMagicdoc(ctx context.Context, eng *engine.Engine, args []string) {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "usage: /magicdoc <target> - describe a file, package, or symbol")
		return
	}
	streamAskToStdout(ctx, eng, "Generate documentation for: "+query, "magicdoc error")
}
