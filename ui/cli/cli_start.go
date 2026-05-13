// cli_start.go — `dfmc start` project bootstrapper.
//
// Designed for the "opened DFMC in an empty folder" scenario:
// detects whether git is installed and the folder has a repo,
// offers to run `git init`, writes a starter `.gitignore`,
// then delegates to `dfmc init` for the .dfmc/ scaffold.
//
// Flow:
//   1. Check `git` binary on PATH.
//   2. Check `.git/` existence.
//   3. Prompt → git init (optional, skip with --yes).
//   4. Offer language-aware .gitignore template (detect or ask).
//   5. Run dfmc init scaffold.

package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// -------------------------------------------------------------------
// gitignore templates — keyed by language / stack.
// -------------------------------------------------------------------

var gitignoreTemplates = map[string]string{
	"go": `*.exe
*.exe~
*.dll
*.so
*.dylib
*.test
*.out
vendor/
go.work
/bin/
/dist/
`,
	"python": `__pycache__/
*.py[cod]
*$py.class
.venv/
venv/
env/
dist/
build/
*.egg-info/
htmlcov/
.coverage
.coverage.*
.pytest_cache/
.env
`,
}

func runStartCommand(args []string) int {
	projectRoot := "."
	if len(args) > 0 {
		projectRoot = args[0]
	}
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving path: %v\n", err)
		return 1
	}

	// 1. Check git binary.
	gitPath, err := exec.LookPath("git")
	if err != nil {
		fmt.Fprintln(os.Stderr, "git not found on PATH. Install git first.")
		return 1
	}
	_ = gitPath

	// 2. Check .git/ existence.
	gitDir := filepath.Join(abs, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		fmt.Printf("No git repo in %s — run `git init` first.\n", abs)
		return 1
	}

	// 3. Offer .gitignore template.
	writeGitignore(abs)

	fmt.Printf("Project ready at %s. Run `dfmc init` to set up .dfmc/.\n", abs)
	return 0
}

func writeGitignore(root string) {
	gitignore := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(gitignore); err == nil {
		return // already exists
	}

	// Detect language.
	lang := detectLanguage(root)
	tpl, ok := gitignoreTemplates[lang]
	if !ok {
		return
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("No .gitignore found. Write a %s template? [Y/n] ", lang)
	resp, _ := reader.ReadString('\n')
	resp = strings.TrimSpace(strings.ToLower(resp))
	if resp != "" && resp != "y" && resp != "yes" {
		return
	}

	if err := os.WriteFile(gitignore, []byte(tpl), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write .gitignore: %v\n", err)
		return
	}
	fmt.Println("Wrote .gitignore")
}

func detectLanguage(root string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		switch {
		case strings.HasSuffix(name, ".go"):
			return "go"
		case strings.HasSuffix(name, ".py"):
			return "python"
		}
	}
	// Default based on OS.
	if runtime.GOOS == "windows" || runtime.GOOS == "linux" {
		// no strong signal
	}
	return ""
}
