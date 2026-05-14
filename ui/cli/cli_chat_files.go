package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

var errSlashGrepLimit = errors.New("match limit reached")

func handleSlashList(eng *engine.Engine, args []string) {
	target := "."
	if len(args) > 0 {
		target = args[0]
	}
	root := eng.Status().ProjectRoot
	base := target
	if !filepath.IsAbs(target) {
		base = filepath.Join(root, target)
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ls error: %v\n", err)
		return
	}
	for _, e := range entries {
		rel, _ := filepath.Rel(root, filepath.Join(base, e.Name()))
		if e.IsDir() {
			fmt.Printf("  📁 %s/\n", rel)
		} else {
			fmt.Printf("  📄 %s\n", rel)
		}
	}
}

func handleSlashRead(eng *engine.Engine, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: /read <path> [line_start [line_end]]")
		return
	}
	path := args[0]
	st := eng.Status()
	if !filepath.IsAbs(path) && st.ProjectRoot != "" {
		path = filepath.Join(st.ProjectRoot, path)
	}
	start, end := 1, 200
	if len(args) > 1 {
		n, _ := strconv.Atoi(args[1])
		if n > 0 {
			start = n
		}
	}
	if len(args) > 2 {
		n, _ := strconv.Atoi(args[2])
		if n > 0 {
			end = n
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read error: %v\n", err)
		return
	}
	lines := strings.Split(string(data), "\n")
	for i := start - 1; i < len(lines) && i < end; i++ {
		fmt.Printf("%4d  %s\n", i+1, lines[i])
	}
}

func handleSlashGrep(eng *engine.Engine, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: /grep <pattern> [path]")
		return
	}
	re, err := regexp.Compile(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad regex: %v\n", err)
		return
	}

	dir := "."
	if len(args) > 1 {
		dir = args[1]
	}
	root := eng.Status().ProjectRoot
	if !filepath.IsAbs(dir) && root != "" {
		dir = filepath.Join(root, dir)
	}

	count := 0
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "_build":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			if re.MatchString(line) {
				rel, _ := filepath.Rel(root, path)
				if rel == "" || rel == "." {
					rel = path
				}
				fmt.Printf("%s:%d: %s\n", rel, i+1, truncateLine(line, 120))
				count++
				if count >= 50 {
					return errSlashGrepLimit
				}
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errSlashGrepLimit) {
		fmt.Fprintf(os.Stderr, "grep error: %v\n", err)
		return
	}
	if count == 0 {
		fmt.Println("(no matches)")
	}
}

func handleSlashRun(eng *engine.Engine, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: /run <command> [args...]")
		return
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = eng.Status().ProjectRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "run error: %v\n", err)
	}
}
