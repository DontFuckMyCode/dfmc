package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/langintel"
)

func (s *Server) handleLangIntel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	lang := strings.TrimSpace(q.Get("lang"))
	kinds := q.Get("kinds")

	registry := s.engine.LangIntel
	if registry == nil {
		registry = langintel.EmptyRegistry()
	}

	var resp any

	if lang != "" {
		resp = registry.ForLang(lang)
	} else if kinds != "" {
		k := strings.Split(kinds, ",")
		resp = registry.ForKinds(k)
	} else {
		resp = registry
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		fmt.Fprintf(os.Stderr, "dfmc: langintel encode error: %v\n", err)
	}
}