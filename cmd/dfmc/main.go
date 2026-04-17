package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/ui/cli"
)

var version = "dev"

func main() {
	// Single os.Exit at the very top of the call stack so every defer
	// inside run() (signal-handler cancel, engine shutdown) actually
	// fires. The previous shape called os.Exit from three different
	// branches and skipped eng.Shutdown() on the degraded-startup
	// path and on any panic out of cli.Run — leaking the bbolt store
	// lock and any background goroutines the engine owned.
	os.Exit(run())
}

func run() int {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 1
	}

	eng, err := engine.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "engine error: %v\n", err)
		return 1
	}
	// Cover every exit path including init-failure-with-degraded-allow
	// and panic-out-of-cli.Run. Engine.Shutdown is safe to call after
	// a partial Init (it no-ops on subsystems that never started).
	defer eng.Shutdown()

	if err := eng.Init(ctx); err != nil {
		if !allowsDegradedStartup(os.Args[1:]) {
			fmt.Fprintf(os.Stderr, "init error: %s\n", formatInitError(err))
			return 1
		}
		fmt.Fprintf(os.Stderr, "init warning: %s\n", formatInitError(err))
	}
	return cli.Run(ctx, eng, os.Args[1:], version)
}

func allowsDegradedStartup(args []string) bool {
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "-") {
			continue
		}
		switch trimmed {
		case "help", "-h", "--help", "version", "doctor", "completion", "man", "update":
			return true
		default:
			return false
		}
	}
	return true
}

func formatInitError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, storage.ErrStoreLocked) {
		return err.Error() + " Use `dfmc doctor` after closing the other session if you want a deeper diagnosis."
	}
	return err.Error()
}
