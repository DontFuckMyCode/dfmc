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

func TestContextBudgetEndpoint(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/context/budget?q=security+audit+auth")
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
}

func TestContextRecommendEndpoint(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/context/recommend?q=debug+%5B%5Bfile%3Ainternal%2Fauth%2Fservice.go%5D%5D")
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

func TestConversationsEndpoints(t *testing.T) {
	eng := newTestEngine(t)
	_, _ = eng.Ask(context.Background(), "merhaba conversation test")
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

	searchResp, err := http.Get(ts.URL + "/api/v1/conversations/search?q=merhaba&limit=5")
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
	if searchPayload["query"] != "merhaba" {
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
