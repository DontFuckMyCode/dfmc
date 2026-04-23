// Interactive subcommand entry points: ask, chat, tui. The chat-slash
// dispatcher lives in cli_chat_slash.go and the unified-diff helpers
// live in cli_chat_diff.go — this file stays focused on the three
// top-level runners.

package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/ui/tui"
)

func runAsk(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	race := fs.Bool("race", false, "race configured providers concurrently; first success wins")
	raceProviders := fs.String("race-providers", "", "comma-separated provider names to race; defaults to primary+fallbacks when --race is set")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	question := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if question == "" {
		fmt.Fprintln(os.Stderr, "ask requires a question")
		return 2
	}

	if *race {
		candidates := splitCSV(*raceProviders)
		answer, winner, err := eng.AskRaced(ctx, question, candidates)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ask --race failed: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"question":   question,
				"answer":     answer,
				"winner":     winner,
				"candidates": candidates,
				"mode":       "race",
			})
			return 0
		}
		fmt.Printf("(won by %s)\n%s\n", winner, answer)
		return 0
	}

	answer, err := eng.Ask(ctx, question)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask failed: %v\n", err)
		return 1
	}

	if jsonMode {
		_ = printJSON(map[string]any{
			"question": question,
			"answer":   answer,
		})
		return 0
	}

	fmt.Println(answer)
	return 0
}

// splitCSV trims and drops empties from a comma-separated CLI value.
// "a, b ,, c" → ["a", "b", "c"]. Empty input returns nil so the engine
// layer gets a clean "let the router derive candidates" signal.
func splitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func runChat(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	branch := fs.String("branch", "", "start/switch to branch name")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if eng.ConversationActive() == nil {
		_ = eng.ConversationStart()
	}
	if b := strings.TrimSpace(*branch); b != "" {
		if err := eng.ConversationBranchSwitch(b); err != nil {
			if err2 := eng.ConversationBranchCreate(b); err2 != nil {
				fmt.Fprintf(os.Stderr, "branch setup failed: %v\n", err)
				return 1
			}
			if err2 := eng.ConversationBranchSwitch(b); err2 != nil {
				fmt.Fprintf(os.Stderr, "branch switch failed: %v\n", err2)
				return 1
			}
		}
	}

	if jsonMode {
		active := eng.ConversationActive()
		branchName := ""
		if active != nil {
			branchName = active.Branch
		}
		_ = printJSON(map[string]any{
			"status": "chat_started",
			"mode":   "basic_repl",
			"branch": branchName,
		})
		return 0
	}

	fmt.Println("DFMC interactive chat (type /exit to quit)")
	fmt.Println("Type /help for slash commands.")

	scanner := bufio.NewScanner(os.Stdin)
	// Pasting a multi-line prompt or a file snippet can easily exceed
	// bufio's default 64 KiB token limit. Raising the cap to 1 MiB keeps
	// realistic paste sizes working without unbounded memory growth on a
	// malformed stdin.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		select {
		case <-ctx.Done():
			return 0
		default:
		}

		fmt.Print("dfmc> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				if !errors.Is(err, os.ErrClosed) {
					fmt.Fprintf(os.Stderr, "input error: %v\n", err)
				}
			}
			return 0
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			shouldExit, handled := runChatSlash(ctx, eng, line)
			if handled {
				if shouldExit {
					return 0
				}
				continue
			}
		}
		if line == "/exit" || line == "exit" || line == "quit" {
			return 0
		}
		stream, err := eng.StreamAsk(ctx, line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
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
			case "done":
			}
		}
		if printed && !endsWithNL {
			fmt.Println()
		}
	}
}

func runTUI(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	noAltScreen := fs.Bool("no-alt-screen", false, "disable alternate screen mode")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if jsonMode {
		fmt.Fprintln(os.Stderr, "tui does not support --json")
		return 2
	}
	if eng.ConversationActive() == nil {
		_ = eng.ConversationStart()
	}
	if err := tui.Run(ctx, eng, tui.Options{AltScreen: !*noAltScreen}); err != nil {
		fmt.Fprintf(os.Stderr, "tui failed: %v\n", err)
		return 1
	}
	return 0
}

