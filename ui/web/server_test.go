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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
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
	t.Cleanup(func() { _ = eng.Shutdown() })
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

func TestStatusEndpointIncludesProviderAdvisories(t *testing.T) {
	eng := newTestEngine(t)
	eng.Config.Providers.Primary = "zai"
	eng.Config.Providers.Profiles["zai"] = config.ModelConfig{
		Model:    "glm-4.5",
		Protocol: "anthropic",
		BaseURL:  "https://api.z.ai/api/anthropic",
	}
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/status")
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	defer resp.Body.Close()

	var payload struct {
		ProviderProfile struct {
			Name       string   `json:"name"`
			Advisories []string `json:"advisories"`
		} `json:"provider_profile"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if payload.ProviderProfile.Name != "zai" {
		t.Fatalf("expected active provider profile zai, got %#v", payload.ProviderProfile)
	}
	if len(payload.ProviderProfile.Advisories) == 0 {
		t.Fatalf("expected provider advisories in status payload: %#v", payload.ProviderProfile)
	}
}

func TestStatusEndpointIncludesApprovalGateAndHooks(t *testing.T) {
	eng := newTestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"write_file", "run_command"}
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/status")
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	defer resp.Body.Close()

	var payload struct {
		ApprovalGate struct {
			Active   bool     `json:"active"`
			Wildcard bool     `json:"wildcard"`
			Count    int      `json:"count"`
			Tools    []string `json:"tools"`
		} `json:"approval_gate"`
		Hooks struct {
			Total    int            `json:"total"`
			PerEvent map[string]int `json:"per_event"`
		} `json:"hooks"`
		RecentDenials int `json:"recent_denials"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if !payload.ApprovalGate.Active || payload.ApprovalGate.Count != 2 {
		t.Fatalf("expected active gate with 2 tools, got %#v", payload.ApprovalGate)
	}
	if payload.ApprovalGate.Wildcard {
		t.Fatalf("wildcard must be false for explicit list")
	}
	if payload.Hooks.Total < 0 {
		t.Fatalf("hooks total must never be negative: %d", payload.Hooks.Total)
	}
	if payload.RecentDenials != 0 {
		t.Fatalf("fresh engine must have zero denials, got %d", payload.RecentDenials)
	}
}

func TestToolSpecEndpoint(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/tools/read_file")
	if err != nil {
		t.Fatalf("get tool spec: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	var spec struct {
		Name string `json:"name"`
		Risk string `json:"risk"`
		Args []struct {
			Name     string `json:"name"`
			Type     string `json:"type"`
			Required bool   `json:"required"`
		} `json:"args"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&spec); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	if spec.Name != "read_file" || spec.Risk == "" {
		t.Fatalf("unexpected spec payload: %#v", spec)
	}
	if len(spec.Args) == 0 {
		t.Fatalf("read_file should advertise at least one arg")
	}
}

func TestToolSpecEndpoint_UnknownTool(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/tools/definitely-not-real")
	if err != nil {
		t.Fatalf("get tool spec: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown tool, got %d", resp.StatusCode)
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

func TestIndexWorkbench(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("get index: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	html := string(body)
	if !strings.Contains(html, "DFMC Workbench") {
		t.Fatalf("expected workbench heading, got: %s", html)
	}
	if !strings.Contains(html, "Live Chat") {
		t.Fatalf("expected live chat panel, got: %s", html)
	}
	if !strings.Contains(html, "CodeMap Pulse") {
		t.Fatalf("expected codemap panel, got: %s", html)
	}
	if !strings.Contains(html, "Patch Lab") {
		t.Fatalf("expected patch lab panel, got: %s", html)
	}
	// Activity panel + its JS wiring must ship so the observability story is
	// symmetric with the TUI. If a refactor drops the firehose handler these
	// guards catch it.
	if !strings.Contains(html, `id="activity-log"`) {
		t.Fatalf("expected activity log container, got: %s", html)
	}
	if !strings.Contains(html, `id="metric-gate"`) {
		t.Fatalf("expected gate metric placeholder, got: %s", html)
	}
	if !strings.Contains(html, `id="metric-hooks"`) {
		t.Fatalf("expected hooks metric placeholder, got: %s", html)
	}
	if !strings.Contains(html, "function classifyActivityEvent") {
		t.Fatalf("activity classifier not inlined: %s", html)
	}
	if !strings.Contains(html, "connectActivityStream()") {
		t.Fatalf("activity stream bootstrap missing: %s", html)
	}
	// The activity renderer must use DOM APIs only — event payloads are
	// untrusted and we've seen other renderers in the file assign innerHTML
	// for other (trusted) content, so we scope the check to the activity
	// function body.
	start := strings.Index(html, "function renderActivityLog")
	if start < 0 {
		t.Fatalf("renderActivityLog function missing")
	}
	end := strings.Index(html[start:], "\nfunction ")
	if end < 0 {
		end = len(html) - start
	}
	activityBody := html[start : start+end]
	forbiddenHTMLSink := "." + "innerHTML"
	if strings.Contains(activityBody, forbiddenHTMLSink) {
		t.Fatalf("renderActivityLog touches innerHTML — must use textContent/appendChild for untrusted payloads")
	}
}

func TestWebSocketEventStreamShape(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/ws?type=*", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open ws: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("unexpected content-type: %q", ct)
	}

	// Publish after a small delay so the subscribe has time to attach.
	go func() {
		time.Sleep(50 * time.Millisecond)
		eng.EventBus.Publish(engine.Event{
			Type:    "tool:call",
			Source:  "test",
			Payload: map[string]any{"tool": "read_file", "step": 2},
		})
	}()

	seen := false
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	deadline := time.Now().Add(3 * time.Second)
	for !seen {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for tool:call frame")
		}
		if !scanner.Scan() {
			t.Fatalf("stream closed before event arrived: %v", scanner.Err())
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
			t.Fatalf("decode frame %q: %v", line, err)
		}
		if frame["type"] != "event" {
			continue
		}
		// Skip unrelated startup events (engine:*, index:*, etc.) — we only
		// care that the shape matches for a specific tool:call.
		if frame["event"] != "tool:call" {
			continue
		}
		payload, ok := frame["payload"].(map[string]any)
		if !ok {
			t.Fatalf("payload not a map: %T %v", frame["payload"], frame["payload"])
		}
		if payload["tool"] != "read_file" {
			t.Fatalf("payload tool=%v", payload["tool"])
		}
		seen = true
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

func TestProvidersEndpointIncludesProviderAdvisories(t *testing.T) {
	eng := newTestEngine(t)
	eng.Config.Providers.Profiles["zai"] = config.ModelConfig{
		Model:    "glm-4.5",
		Protocol: "anthropic",
		BaseURL:  "https://api.z.ai/api/anthropic",
	}
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/providers")
	if err != nil {
		t.Fatalf("get providers: %v", err)
	}
	defer resp.Body.Close()

	var payload struct {
		Providers []struct {
			Name       string   `json:"name"`
			Advisories []string `json:"advisories"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	for _, item := range payload.Providers {
		if item.Name != "zai" {
			continue
		}
		if len(item.Advisories) == 0 {
			t.Fatalf("expected zai provider advisories, got %#v", item)
		}
		return
	}
	t.Fatalf("zai provider missing from payload: %#v", payload.Providers)
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

func TestContextBudgetEndpoint(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/context/budget?q=security+audit+auth&runtime_max_context=1000")
	if err != nil {
		t.Fatalf("get context budget: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if payload["task"] != "security" {
		t.Fatalf("expected task=security, got: %#v", payload["task"])
	}
	if _, ok := payload["max_tokens_total"]; !ok {
		t.Fatalf("missing max_tokens_total field: %#v", payload)
	}
	if _, ok := payload["context_available_tokens"]; !ok {
		t.Fatalf("missing context_available_tokens field: %#v", payload)
	}
	if _, ok := payload["reserve_total_tokens"]; !ok {
		t.Fatalf("missing reserve_total_tokens field: %#v", payload)
	}
	if _, ok := payload["explicit_file_mentions"]; !ok {
		t.Fatalf("missing explicit_file_mentions field: %#v", payload)
	}
	if _, ok := payload["task_total_scale"]; !ok {
		t.Fatalf("missing task_total_scale field: %#v", payload)
	}
	if got, _ := payload["provider_max_context"].(float64); int(got) != 1000 {
		t.Fatalf("expected provider_max_context=1000, got: %#v", payload["provider_max_context"])
	}
}

func TestContextRecommendEndpoint(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/context/recommend?q=debug+%5B%5Bfile%3Ainternal%2Fauth%2Fservice.go%5D%5D&runtime_max_context=1000")
	if err != nil {
		t.Fatalf("get context recommend: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if _, ok := payload["preview"]; !ok {
		t.Fatalf("missing preview field: %#v", payload)
	}
	recs, ok := payload["recommendations"].([]any)
	if !ok || len(recs) == 0 {
		t.Fatalf("expected non-empty recommendations, got: %#v", payload["recommendations"])
	}
	tuning, ok := payload["tuning_suggestions"].([]any)
	if !ok || len(tuning) == 0 {
		t.Fatalf("expected non-empty tuning_suggestions, got: %#v", payload["tuning_suggestions"])
	}
	preview, ok := payload["preview"].(map[string]any)
	if !ok {
		t.Fatalf("expected preview object, got: %#v", payload["preview"])
	}
	if got, _ := preview["provider_max_context"].(float64); int(got) != 1000 {
		t.Fatalf("expected preview provider_max_context=1000, got: %#v", preview["provider_max_context"])
	}
}

func TestContextBriefEndpoint(t *testing.T) {
	eng := newTestEngine(t)
	root := t.TempDir()
	eng.ProjectRoot = root

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "docs", "BRIEF.md"),
		[]byte("# MAGIC DOC: Custom Brief\n\nContext brief endpoint smoke line.\n"),
		0o644,
	); err != nil {
		t.Fatalf("write brief file: %v", err)
	}

	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/context/brief?path=docs/BRIEF.md&max_words=20")
	if err != nil {
		t.Fatalf("get context brief: %v", err)
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
	if payload["exists"] != true {
		t.Fatalf("expected exists=true, got: %#v", payload)
	}
	if payload["max_words"] != float64(20) {
		t.Fatalf("expected max_words=20, got: %#v", payload["max_words"])
	}
	path, _ := payload["path"].(string)
	if !strings.Contains(path, "/docs/BRIEF.md") {
		t.Fatalf("expected custom path in payload, got: %s", path)
	}
	brief, _ := payload["brief"].(string)
	if !strings.Contains(brief, "Context brief endpoint smoke line.") {
		t.Fatalf("expected brief content from custom file, got: %s", brief)
	}
	wordCount, _ := payload["word_count"].(float64)
	if wordCount <= 0 || wordCount > 20 {
		t.Fatalf("unexpected word_count: %#v", payload["word_count"])
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

func TestAnalyzeEndpointWithMagicDoc(t *testing.T) {
	eng := newTestEngine(t)
	root := t.TempDir()
	eng.ProjectRoot = root
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := bytes.NewBufferString(`{"complexity":true,"magicdoc":true,"magicdoc_title":"Analyze Brief"}`)
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
	if _, ok := payload["report"]; !ok {
		t.Fatalf("missing report field: %#v", payload)
	}
	magic, ok := payload["magicdoc"].(map[string]any)
	if !ok {
		t.Fatalf("missing magicdoc field: %#v", payload)
	}
	if magic["updated"] != true {
		t.Fatalf("expected magicdoc updated=true, got: %#v", magic)
	}
}

func TestFileContentAndToolExecEndpoints(t *testing.T) {
	t.Setenv("DFMC_APPROVE", "yes")
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
	if !strings.Contains(output, "world") {
		t.Fatalf("unexpected tool output: %q", output)
	}
	if !strings.Contains(output, "[truncated -") {
		t.Fatalf("expected visible truncation marker in tool output, got: %q", output)
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

func TestConversationsEndpoints(t *testing.T) {
	eng := newTestEngine(t)
	_, _ = eng.Ask(context.Background(), "hello conversation test")
	_ = eng.ConversationSave()

	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/conversations")
	if err != nil {
		t.Fatalf("get conversations: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, string(raw))
	}
	var listPayload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&listPayload); err != nil {
		t.Fatalf("decode list payload: %v", err)
	}
	if _, ok := listPayload["conversations"]; !ok {
		t.Fatalf("missing conversations field: %#v", listPayload)
	}

	searchResp, err := http.Get(ts.URL + "/api/v1/conversations/search?q=hello&limit=5")
	if err != nil {
		t.Fatalf("search conversations: %v", err)
	}
	defer searchResp.Body.Close()
	if searchResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(searchResp.Body)
		t.Fatalf("unexpected status %d: %s", searchResp.StatusCode, string(raw))
	}
	var searchPayload map[string]any
	if err := json.NewDecoder(searchResp.Body).Decode(&searchPayload); err != nil {
		t.Fatalf("decode search payload: %v", err)
	}
	if searchPayload["query"] != "hello" {
		t.Fatalf("unexpected query field: %#v", searchPayload)
	}

	activeResp, err := http.Get(ts.URL + "/api/v1/conversation")
	if err != nil {
		t.Fatalf("get active conversation: %v", err)
	}
	defer activeResp.Body.Close()
	if activeResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(activeResp.Body)
		t.Fatalf("unexpected status %d: %s", activeResp.StatusCode, string(raw))
	}

	newReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/conversation/new", bytes.NewBufferString(`{}`))
	newReq.Header.Set("Content-Type", "application/json")
	newResp, err := http.DefaultClient.Do(newReq)
	if err != nil {
		t.Fatalf("new conversation request: %v", err)
	}
	defer newResp.Body.Close()
	if newResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(newResp.Body)
		t.Fatalf("unexpected status %d: %s", newResp.StatusCode, string(raw))
	}

	createBranchReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/conversation/branches/create", bytes.NewBufferString(`{"name":"alt"}`))
	createBranchReq.Header.Set("Content-Type", "application/json")
	createBranchResp, err := http.DefaultClient.Do(createBranchReq)
	if err != nil {
		t.Fatalf("create branch request: %v", err)
	}
	defer createBranchResp.Body.Close()
	if createBranchResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(createBranchResp.Body)
		t.Fatalf("unexpected status %d: %s", createBranchResp.StatusCode, string(raw))
	}

	switchBranchReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/conversation/branches/switch", bytes.NewBufferString(`{"name":"alt"}`))
	switchBranchReq.Header.Set("Content-Type", "application/json")
	switchBranchResp, err := http.DefaultClient.Do(switchBranchReq)
	if err != nil {
		t.Fatalf("switch branch request: %v", err)
	}
	defer switchBranchResp.Body.Close()
	if switchBranchResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(switchBranchResp.Body)
		t.Fatalf("unexpected status %d: %s", switchBranchResp.StatusCode, string(raw))
	}

	compareResp, err := http.Get(ts.URL + "/api/v1/conversation/branches/compare?a=main&b=alt")
	if err != nil {
		t.Fatalf("compare branch request: %v", err)
	}
	defer compareResp.Body.Close()
	if compareResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(compareResp.Body)
		t.Fatalf("unexpected status %d: %s", compareResp.StatusCode, string(raw))
	}
}

func TestWorkspaceEndpoints(t *testing.T) {
	t.Setenv("DFMC_APPROVE", "yes")
	t.Setenv("DFMC_APPROVE_DESTRUCTIVE", "yes")
	eng := newTestEngine(t)
	root := t.TempDir()
	run := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		return cmd.Run()
	}
	if err := run("init"); err != nil {
		t.Skipf("git is unavailable: %v", err)
	}
	_ = run("config", "user.name", "dfmc-test")
	_ = run("config", "user.email", "dfmc@test.local")

	aPath := filepath.Join(root, "a.txt")
	bPath := filepath.Join(root, "b.txt")
	if err := os.WriteFile(aPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(bPath, []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	_ = run("add", "a.txt", "b.txt")
	_ = run("commit", "-m", "init")
	if err := os.WriteFile(bPath, []byte("base\nlocal\n"), 0o644); err != nil {
		t.Fatalf("rewrite b.txt: %v", err)
	}

	eng.ProjectRoot = root
	_ = eng.ConversationStart()
	eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:    types.RoleUser,
		Content: "please produce a patch",
	})
	eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:    types.RoleAssistant,
		Content: "```diff\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1,2 @@\n hello\n+world\n```\n",
	})

	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	diffResp, err := http.Get(ts.URL + "/api/v1/workspace/diff")
	if err != nil {
		t.Fatalf("get workspace diff: %v", err)
	}
	defer diffResp.Body.Close()
	if diffResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(diffResp.Body)
		t.Fatalf("unexpected diff status %d: %s", diffResp.StatusCode, string(raw))
	}
	var diffPayload map[string]any
	if err := json.NewDecoder(diffResp.Body).Decode(&diffPayload); err != nil {
		t.Fatalf("decode diff payload: %v", err)
	}
	if diffPayload["clean"] != false {
		t.Fatalf("expected dirty worktree, got %#v", diffPayload)
	}
	if !strings.Contains(diffPayload["diff"].(string), "b.txt") {
		t.Fatalf("expected b.txt in diff payload, got %#v", diffPayload["diff"])
	}

	patchResp, err := http.Get(ts.URL + "/api/v1/workspace/patch")
	if err != nil {
		t.Fatalf("get workspace patch: %v", err)
	}
	defer patchResp.Body.Close()
	var patchPayload map[string]any
	if err := json.NewDecoder(patchResp.Body).Decode(&patchPayload); err != nil {
		t.Fatalf("decode patch payload: %v", err)
	}
	if patchPayload["available"] != true {
		t.Fatalf("expected available patch, got %#v", patchPayload)
	}
	patchText, _ := patchPayload["patch"].(string)
	if !strings.Contains(patchText, "+++ b/a.txt") {
		t.Fatalf("expected unified diff in patch payload, got %q", patchText)
	}

	checkReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/workspace/apply", bytes.NewBufferString(`{"source":"latest","check_only":true}`))
	if err != nil {
		t.Fatalf("new check request: %v", err)
	}
	checkReq.Header.Set("Content-Type", "application/json")
	checkResp, err := http.DefaultClient.Do(checkReq)
	if err != nil {
		t.Fatalf("check patch request: %v", err)
	}
	defer checkResp.Body.Close()
	if checkResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(checkResp.Body)
		t.Fatalf("unexpected check status %d: %s", checkResp.StatusCode, string(raw))
	}

	// Read a.txt via the tool API so the read-before-mutation gate allows the apply.
	readReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/tools/read_file", bytes.NewBufferString(`{"params":{"path":"a.txt"}}`))
	if err != nil {
		t.Fatalf("new read request: %v", err)
	}
	readReq.Header.Set("Content-Type", "application/json")
	readResp, err := http.DefaultClient.Do(readReq)
	if err != nil {
		t.Fatalf("read a.txt: %v", err)
	}
	defer readResp.Body.Close()
	if readResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(readResp.Body)
		t.Fatalf("read a.txt status %d: %s", readResp.StatusCode, string(raw))
	}

	applyReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/workspace/apply", bytes.NewBufferString(`{"source":"latest"}`))
	if err != nil {
		t.Fatalf("new apply request: %v", err)
	}
	applyReq.Header.Set("Content-Type", "application/json")
	applyResp, err := http.DefaultClient.Do(applyReq)
	if err != nil {
		t.Fatalf("apply patch request: %v", err)
	}
	defer applyResp.Body.Close()
	if applyResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(applyResp.Body)
		t.Fatalf("unexpected apply status %d: %s", applyResp.StatusCode, string(raw))
	}
	applied, err := os.ReadFile(aPath)
	if err != nil {
		t.Fatalf("read applied file: %v", err)
	}
	if !strings.Contains(string(applied), "world") {
		t.Fatalf("expected patch to modify a.txt, got: %s", string(applied))
	}

	undoReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/conversation/undo", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("new undo request: %v", err)
	}
	undoResp, err := http.DefaultClient.Do(undoReq)
	if err != nil {
		t.Fatalf("undo request: %v", err)
	}
	defer undoResp.Body.Close()
	if undoResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(undoResp.Body)
		t.Fatalf("unexpected undo status %d: %s", undoResp.StatusCode, string(raw))
	}
	var undoPayload map[string]any
	if err := json.NewDecoder(undoResp.Body).Decode(&undoPayload); err != nil {
		t.Fatalf("decode undo payload: %v", err)
	}
	if undoPayload["removed"] != float64(2) {
		t.Fatalf("expected removed=2, got %#v", undoPayload)
	}
}

func TestPromptsEndpoints(t *testing.T) {
	eng := newTestEngine(t)
	root := t.TempDir()
	eng.ProjectRoot = root
	magicDir := filepath.Join(root, ".dfmc", "magic")
	if err := os.MkdirAll(magicDir, 0o755); err != nil {
		t.Fatalf("mkdir magic dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(magicDir, "MAGIC_DOC.md"), []byte("# MAGIC DOC: Prompt Brief\n\nCritical prompt brief line.\n"), 0o644); err != nil {
		t.Fatalf("write magic doc: %v", err)
	}

	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	listResp, err := http.Get(ts.URL + "/api/v1/prompts")
	if err != nil {
		t.Fatalf("get prompts: %v", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(listResp.Body)
		t.Fatalf("unexpected status %d: %s", listResp.StatusCode, string(raw))
	}
	var listPayload map[string]any
	if err := json.NewDecoder(listResp.Body).Decode(&listPayload); err != nil {
		t.Fatalf("decode list payload: %v", err)
	}
	if _, ok := listPayload["prompts"]; !ok {
		t.Fatalf("missing prompts field: %#v", listPayload)
	}

	statsResp, err := http.Get(ts.URL + "/api/v1/prompts/stats?max_template_tokens=200")
	if err != nil {
		t.Fatalf("get prompt stats: %v", err)
	}
	defer statsResp.Body.Close()
	if statsResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(statsResp.Body)
		t.Fatalf("unexpected stats status %d: %s", statsResp.StatusCode, string(raw))
	}
	var statsPayload map[string]any
	if err := json.NewDecoder(statsResp.Body).Decode(&statsPayload); err != nil {
		t.Fatalf("decode stats payload: %v", err)
	}
	if statsPayload["template_count"] == nil {
		t.Fatalf("missing template_count in stats payload: %#v", statsPayload)
	}
	if statsPayload["max_template_tokens"] != float64(200) {
		t.Fatalf("expected max_template_tokens=200, got: %#v", statsPayload["max_template_tokens"])
	}

	recommendResp, err := http.Get(ts.URL + "/api/v1/prompts/recommend?q=security+audit+auth&runtime_tool_style=function-calling&runtime_max_context=1000")
	if err != nil {
		t.Fatalf("get prompt recommend: %v", err)
	}
	defer recommendResp.Body.Close()
	if recommendResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(recommendResp.Body)
		t.Fatalf("unexpected recommend status %d: %s", recommendResp.StatusCode, string(raw))
	}
	var recommendPayload map[string]any
	if err := json.NewDecoder(recommendResp.Body).Decode(&recommendPayload); err != nil {
		t.Fatalf("decode recommend payload: %v", err)
	}
	rec, ok := recommendPayload["recommendation"].(map[string]any)
	if !ok {
		t.Fatalf("missing recommendation field: %#v", recommendPayload)
	}
	if rec["prompt_budget_tokens"] == nil {
		t.Fatalf("missing prompt_budget_tokens in recommendation payload: %#v", recommendPayload)
	}
	if got, _ := rec["tool_style"].(string); got != "function-calling" {
		t.Fatalf("expected tool_style override in recommendation payload, got: %#v", rec["tool_style"])
	}
	if got, _ := rec["max_context"].(float64); int(got) != 1000 {
		t.Fatalf("expected max_context override in recommendation payload, got: %#v", rec["max_context"])
	}

	renderBody := bytes.NewBufferString(`{"type":"system","task":"security","query":"auth security audit","runtime_tool_style":"function-calling","runtime_max_context":1000}`)
	renderReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/prompts/render", renderBody)
	if err != nil {
		t.Fatalf("new render request: %v", err)
	}
	renderReq.Header.Set("Content-Type", "application/json")
	renderResp, err := http.DefaultClient.Do(renderReq)
	if err != nil {
		t.Fatalf("render request: %v", err)
	}
	defer renderResp.Body.Close()
	if renderResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(renderResp.Body)
		t.Fatalf("unexpected status %d: %s", renderResp.StatusCode, string(raw))
	}
	var renderPayload map[string]any
	if err := json.NewDecoder(renderResp.Body).Decode(&renderPayload); err != nil {
		t.Fatalf("decode render payload: %v", err)
	}
	prompt, _ := renderPayload["prompt"].(string)
	if !strings.Contains(strings.ToLower(prompt), "security") {
		t.Fatalf("expected security prompt, got: %s", prompt)
	}
	if !strings.Contains(prompt, "Critical prompt brief line.") {
		t.Fatalf("expected project brief injection in prompt render, got: %s", prompt)
	}
	if !strings.Contains(prompt, "strict function-call JSON") {
		t.Fatalf("expected runtime tool-style override in prompt render, got: %s", prompt)
	}
	if !strings.Contains(prompt, "near 200 tokens") {
		t.Fatalf("expected runtime max context budget in prompt render, got: %s", prompt)
	}
	if role, _ := renderPayload["role"].(string); role == "" {
		t.Fatalf("expected role in render payload, got: %#v", renderPayload)
	}
	if _, ok := renderPayload["prompt_tokens_estimate"].(float64); !ok {
		t.Fatalf("expected prompt_tokens_estimate in payload, got: %#v", renderPayload)
	}
	if _, ok := renderPayload["prompt_budget_tokens"].(float64); !ok {
		t.Fatalf("expected prompt_budget_tokens in payload, got: %#v", renderPayload)
	}
}

func TestMagicDocEndpoints(t *testing.T) {
	eng := newTestEngine(t)
	root := t.TempDir()
	eng.ProjectRoot = root
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	updateBody := bytes.NewBufferString(`{"title":"Remote Brief","hotspots":4,"deps":4,"recent":3}`)
	updateReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/magicdoc/update", updateBody)
	if err != nil {
		t.Fatalf("new update request: %v", err)
	}
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp, err := http.DefaultClient.Do(updateReq)
	if err != nil {
		t.Fatalf("magicdoc update request: %v", err)
	}
	defer updateResp.Body.Close()
	if updateResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(updateResp.Body)
		t.Fatalf("unexpected update status %d: %s", updateResp.StatusCode, string(raw))
	}

	showResp, err := http.Get(ts.URL + "/api/v1/magicdoc")
	if err != nil {
		t.Fatalf("magicdoc show request: %v", err)
	}
	defer showResp.Body.Close()
	if showResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(showResp.Body)
		t.Fatalf("unexpected show status %d: %s", showResp.StatusCode, string(raw))
	}
	var showPayload map[string]any
	if err := json.NewDecoder(showResp.Body).Decode(&showPayload); err != nil {
		t.Fatalf("decode show payload: %v", err)
	}
	content, _ := showPayload["content"].(string)
	if !strings.Contains(content, "# MAGIC DOC: Remote Brief") {
		t.Fatalf("expected magic doc title in content, got: %s", content)
	}
}

func TestCommandsEndpoint(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Catalog.
	resp, err := http.Get(ts.URL + "/api/v1/commands")
	if err != nil {
		t.Fatalf("get commands: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	var payload struct {
		Groups []struct {
			Category string            `json:"category"`
			Label    string            `json:"label"`
			Commands []json.RawMessage `json:"commands"`
		} `json:"groups"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode commands json: %v", err)
	}
	if len(payload.Groups) == 0 {
		t.Fatalf("expected at least one group, got none")
	}
	seenQuery := false
	for _, g := range payload.Groups {
		if g.Label == "" {
			t.Fatalf("group missing label: %+v", g)
		}
		if g.Category == "query" {
			seenQuery = true
		}
	}
	if !seenQuery {
		t.Fatalf("expected `query` category in web surface, got: %+v", payload.Groups)
	}

	// Detail lookup via alias (conv -> conversation).
	detailResp, err := http.Get(ts.URL + "/api/v1/commands/conv")
	if err != nil {
		t.Fatalf("get command detail: %v", err)
	}
	defer detailResp.Body.Close()
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("detail status: %d", detailResp.StatusCode)
	}
	var detail map[string]any
	if err := json.NewDecoder(detailResp.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail["name"] != "conversation" {
		t.Fatalf("alias should resolve to canonical name, got %v", detail["name"])
	}

	// 404 for unknown.
	missResp, err := http.Get(ts.URL + "/api/v1/commands/definitely-not-a-command")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	defer missResp.Body.Close()
	if missResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", missResp.StatusCode)
	}
}
