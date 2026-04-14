package cli

import "testing"

func TestBrowserCommandForOS(t *testing.T) {
	target := "http://127.0.0.1:7788"

	name, args, ok := browserCommandForOS("windows", target)
	if !ok || name != "cmd" {
		t.Fatalf("windows command mismatch: ok=%v name=%s args=%v", ok, name, args)
	}
	if len(args) < 4 || args[0] != "/c" || args[1] != "start" {
		t.Fatalf("windows args mismatch: %v", args)
	}

	name, args, ok = browserCommandForOS("darwin", target)
	if !ok || name != "open" || len(args) != 1 || args[0] != target {
		t.Fatalf("darwin command mismatch: ok=%v name=%s args=%v", ok, name, args)
	}

	name, args, ok = browserCommandForOS("linux", target)
	if !ok || name != "xdg-open" || len(args) != 1 || args[0] != target {
		t.Fatalf("linux command mismatch: ok=%v name=%s args=%v", ok, name, args)
	}

	_, _, ok = browserCommandForOS("plan9", target)
	if ok {
		t.Fatal("expected unsupported platform to return ok=false")
	}
}
