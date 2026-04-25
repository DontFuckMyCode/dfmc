package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestHandleFileContent_RedactsSecretLeaves pins VULN-013: the web
// file API used to return raw bytes for any in-root path, leaking
// `.env`, `id_rsa`, `credentials.json` etc. The handler must now
// substitute a redacted=true response instead of file content.
func TestHandleFileContent_RedactsSecretLeaves(t *testing.T) {
	eng := newTestEngine(t)
	root := t.TempDir()
	eng.ProjectRoot = root

	// Write a `.env` file with what looks like a real key.
	envPath := filepath.Join(root, ".env")
	envBody := "ANTHROPIC_API_KEY=sk-ant-not-actually-a-real-key-just-fixture\n"
	if err := os.WriteFile(envPath, []byte(envBody), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	// And a non-secret file as a control — must still be served raw.
	if err := os.WriteFile(filepath.Join(root, "readme.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}

	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Secret leaf — must be redacted.
	resp, err := http.Get(ts.URL + "/api/v1/files/.env")
	if err != nil {
		t.Fatalf("get .env: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with redacted body, got %d", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["redacted"] != true {
		t.Errorf("expected redacted=true on .env response, got %#v", got)
	}
	if content, _ := got["content"].(string); content != "" {
		t.Errorf(".env content must be empty when redacted, got %q", content)
	}
	// Stat-based size should still be reported so the UI can show "X bytes hidden".
	if size, _ := got["size"].(float64); size <= 0 {
		t.Errorf("expected size>0 on redacted .env, got %v", got["size"])
	}

	// Non-secret leaf — must still serve raw bytes.
	resp2, err := http.Get(ts.URL + "/api/v1/files/readme.md")
	if err != nil {
		t.Fatalf("get readme.md: %v", err)
	}
	defer resp2.Body.Close()
	var got2 map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&got2); err != nil {
		t.Fatalf("decode readme: %v", err)
	}
	if got2["redacted"] == true {
		t.Errorf("readme.md must NOT be redacted, got %#v", got2)
	}
	if content, _ := got2["content"].(string); content != "hi\n" {
		t.Errorf("readme.md content mismatch: got %q", content)
	}
}

// TestHandleFileContent_RedactsAllKnownSecretShapes covers the
// canonical secret-name shapes through the HTTP boundary so a future
// regression in either the predicate or the handler trips here.
func TestHandleFileContent_RedactsAllKnownSecretShapes(t *testing.T) {
	eng := newTestEngine(t)
	root := t.TempDir()
	eng.ProjectRoot = root

	files := []string{
		".env",
		".env.production",
		"id_rsa",
		"id_ed25519",
		"credentials.json",
		"secrets.yaml",
		"server.pem",
		"vault.kdbx",
		"some_apikey.txt",
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte("secret-payload\n"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			resp, err := http.Get(ts.URL + "/api/v1/files/" + name)
			if err != nil {
				t.Fatalf("get %s: %v", name, err)
			}
			defer resp.Body.Close()
			var got map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatalf("decode %s: %v", name, err)
			}
			if got["redacted"] != true {
				t.Errorf("%s must be redacted, got %#v", name, got)
			}
			if content, _ := got["content"].(string); content != "" {
				t.Errorf("%s content leaked: %q", name, content)
			}
		})
	}
}
