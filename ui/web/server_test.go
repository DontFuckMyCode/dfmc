package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func newTestEngine(t *testing.T) *engine.Engine {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("HOME", tmp)

	cfg := config.DefaultConfig()
	cfg.Web.Host = "127.0.0.1"
	cfg.Web.Port = 0

	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("init engine: %v", err)
	}
	eng.ProjectRoot = filepath.Clean(".")
	t.Cleanup(func() { eng.Shutdown() })
	return eng
}

func TestStatusEndpoint(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/status")
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if _, ok := payload["state"]; !ok {
		t.Fatalf("missing state field: %#v", payload)
	}
}

func TestHealthEndpoint(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("get healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestChatSSEEndpoint(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := bytes.NewBufferString(`{"message":"Sadece OK yaz"}`)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/chat", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chat request: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("unexpected content-type: %s", ct)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, `"type":"delta"`) {
		t.Fatalf("expected delta event, got: %s", text)
	}
	if !strings.Contains(text, `"type":"done"`) {
		t.Fatalf("expected done event, got: %s", text)
	}
}

func TestProvidersEndpoint(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/providers")
	if err != nil {
		t.Fatalf("get providers: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if _, ok := payload["providers"]; !ok {
		t.Fatalf("missing providers field: %#v", payload)
	}
}

func TestSkillsEndpoint(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/skills")
	if err != nil {
		t.Fatalf("get skills: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	skillsRaw, ok := payload["skills"]
	if !ok {
		t.Fatalf("missing skills field: %#v", payload)
	}
	skills, ok := skillsRaw.([]any)
	if !ok || len(skills) == 0 {
		t.Fatalf("expected non-empty skills list, got: %#v", skillsRaw)
	}
}

func TestAnalyzeEndpoint(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := bytes.NewBufferString(`{"complexity":true}`)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/analyze", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("analyze request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, string(raw))
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if _, ok := payload["files"]; !ok {
		t.Fatalf("missing files field: %#v", payload)
	}
}

func TestFileContentAndToolExecEndpoints(t *testing.T) {
	eng := newTestEngine(t)
	root := t.TempDir()
	eng.ProjectRoot = root
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}

	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/files/sample.txt")
	if err != nil {
		t.Fatalf("get file content: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, string(raw))
	}
	var filePayload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&filePayload); err != nil {
		t.Fatalf("decode file payload: %v", err)
	}
	content, _ := filePayload["content"].(string)
	if !strings.Contains(content, "hello") {
		t.Fatalf("unexpected file content: %#v", filePayload)
	}

	toolBody := bytes.NewBufferString(`{"params":{"path":"sample.txt","line_start":2,"line_end":2}}`)
	toolReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/tools/read_file", toolBody)
	if err != nil {
		t.Fatalf("new tool request: %v", err)
	}
	toolReq.Header.Set("Content-Type", "application/json")
	toolResp, err := http.DefaultClient.Do(toolReq)
	if err != nil {
		t.Fatalf("tool request: %v", err)
	}
	defer toolResp.Body.Close()
	if toolResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(toolResp.Body)
		t.Fatalf("unexpected tool status %d: %s", toolResp.StatusCode, string(raw))
	}
	var toolPayload map[string]any
	if err := json.NewDecoder(toolResp.Body).Decode(&toolPayload); err != nil {
		t.Fatalf("decode tool payload: %v", err)
	}
	output, _ := toolPayload["output"].(string)
	if strings.TrimSpace(output) != "world" {
		t.Fatalf("unexpected tool output: %q", output)
	}
}

func TestSkillExecEndpoint(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := bytes.NewBufferString(`{"input":"Sadece OK yaz"}`)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/skills/review", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("skill request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, string(raw))
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if _, ok := payload["answer"]; !ok {
		t.Fatalf("missing answer field: %#v", payload)
	}
}

func TestWSEventStreamEndpoint(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ws?type=test:event")
	if err != nil {
		t.Fatalf("ws request: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("unexpected content-type: %s", ct)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		eng.EventBus.Publish(engine.Event{
			Type:    "test:event",
			Source:  "test",
			Payload: map[string]any{"ok": true},
		})
	}()

	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(2 * time.Second)
	foundConnected := false
	foundEvent := false

	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read stream line: %v", err)
		}
		if strings.Contains(line, `"type":"connected"`) {
			foundConnected = true
		}
		if strings.Contains(line, `"event":"test:event"`) {
			foundEvent = true
			break
		}
	}

	if !foundConnected {
		t.Fatal("expected connected event")
	}
	if !foundEvent {
		t.Fatal("expected test:event in stream")
	}
}
