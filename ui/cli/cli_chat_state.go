package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func handleSlashMap(eng *engine.Engine) {
	st := eng.Status()
	fmt.Printf("project root: %s\n", st.ProjectRoot)
	if st.ProjectRoot == "" {
		return
	}
	entries, _ := os.ReadDir(st.ProjectRoot)
	fmt.Println("top-level:")
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !strings.HasPrefix(e.Name(), "_") {
			fmt.Printf("  📁 %s/\n", e.Name())
		} else if !e.IsDir() {
			fmt.Printf("  📄 %s\n", e.Name())
		}
	}
}

func handleSlashConversation(eng *engine.Engine, args []string) {
	sub := strings.TrimSpace(strings.Join(args, " "))
	switch sub {
	case "list", "":
		items, err := eng.ConversationList()
		if err != nil {
			fmt.Fprintf(os.Stderr, "list error: %v\n", err)
			return
		}
		if len(items) == 0 {
			fmt.Println("no saved conversations")
			return
		}
		fmt.Printf("saved conversations (%d):\n", len(items))
		activeConv := eng.ConversationActive()
		for _, it := range items {
			active := ""
			if activeConv != nil && it.ID == activeConv.ID {
				active = " [active]"
			}
			fmt.Printf("  - %s (%d msgs)%s\n", it.ID, it.MessageN, active)
		}
	case "save":
		if err := eng.ConversationSave(); err != nil {
			fmt.Fprintf(os.Stderr, "save error: %v\n", err)
		} else {
			fmt.Println("conversation saved")
		}
	case "clear", "reset":
		eng.ConversationStart()
		fmt.Println("conversation cleared - started fresh")
	default:
		fmt.Fprintf(os.Stderr, "usage: /conversation [list|save|clear]\n")
	}
}

func handleSlashMemory(eng *engine.Engine, args []string) {
	sub := strings.TrimSpace(strings.Join(args, " "))
	switch sub {
	case "show", "":
		w := eng.MemoryWorking()
		fmt.Printf("last question: %s\n", w.LastQuestion)
		fmt.Printf("last answer: %s\n", truncateLine(w.LastAnswer, 120))
		fmt.Printf("recent files (%d):\n", len(w.RecentFiles))
		for _, f := range w.RecentFiles {
			fmt.Printf("  - %s\n", f)
		}
	case "clear":
		fmt.Println("(memory is managed automatically - /clear resets conversation context)")
	default:
		fmt.Fprintf(os.Stderr, "usage: /memory [show|clear]\n")
	}
}

func handleSlashPrompt(eng *engine.Engine, args []string) {
	sub := strings.TrimSpace(strings.Join(args, " "))
	switch sub {
	case "show", "":
		st := eng.Status()
		fmt.Println("active provider/model:")
		fmt.Printf("  provider: %s\n", st.Provider)
		fmt.Printf("  model:    %s\n", st.Model)
		fmt.Println("prompt context: conversation history + project root")
	case "context":
		ctx := eng.MemoryWorking()
		fmt.Printf("working context size estimate: %d recent files\n", len(ctx.RecentFiles))
	default:
		fmt.Fprintf(os.Stderr, "usage: /prompt [show|context]\n")
	}
}

func handleSlashSkill(eng *engine.Engine, args []string) {
	sub := strings.TrimSpace(strings.Join(args, " "))
	if sub == "" || sub == "list" {
		for _, s := range discoverSkills(eng.Status().ProjectRoot) {
			source := s.Source
			if s.Builtin {
				source = "builtin"
			}
			fmt.Printf("- %s [%s]\n", s.Name, source)
		}
		return
	}
	fmt.Printf("(skill '%s' - run 'dfmc skill %s' for details)\n", sub, sub)
}
