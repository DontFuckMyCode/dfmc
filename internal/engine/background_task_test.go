package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestStartBackgroundTaskStopsOnShutdown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".dfmc"), 0o755); err != nil {
		t.Fatalf("mkdir temp user config dir: %v", err)
	}

	cfg := config.DefaultConfig()
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("eng.Init: %v", err)
	}

	started := make(chan struct{})
	stopped := make(chan struct{})
	eng.StartBackgroundTask("test.background", func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(stopped)
	})

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background task did not start")
	}

	_ = eng.Shutdown()

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("background task did not stop on shutdown")
	}
}
