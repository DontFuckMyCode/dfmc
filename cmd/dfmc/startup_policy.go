package main

import (
	"errors"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/storage"
)

// suppressInitWarning silences the "init warning: storage is locked"
// banner for pure meta commands that never touch the store. doctor and
// update legitimately want the lock context as diagnostic signal, but
// help/version/completion/man are read-only catalogs - leading them with
// a scary warning header trains users to ignore the line entirely.
func suppressInitWarning(args []string) bool {
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" || strings.HasPrefix(trimmed, "-") {
			continue
		}
		switch trimmed {
		case "help", "version", "completion", "man":
			return true
		default:
			return false
		}
	}
	return true
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

func unsafeHooksOverrideEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DFMC_UNSAFE_HOOKS"))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
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
