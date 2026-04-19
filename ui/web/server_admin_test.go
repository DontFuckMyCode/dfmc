// Pin tests for the four CLI-parity admin endpoints (scan, doctor,
// hooks, config). Goal: lock the JSON shape that operators / monitoring
// scrapers depend on and prove credentials never leak through GET
// /api/v1/config no matter what the underlying yaml round-trip surfaces.

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// /api/v1/scan must always return a JSON object with a numeric
// files_scanned and at least the secrets/vulnerabilities fields,
// even on an empty project (path defaults to root).
func TestScanEndpoint_ShapeOnEmptyProject(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/scan")
	if err != nil {
		t.Fatalf("get scan: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// files_scanned is always present; secrets/vulnerabilities use
	// omitempty so an empty scan elides them. Accept either.
	if _, ok := payload["files_scanned"]; !ok {
		t.Errorf("scan payload missing files_scanned: %#v", payload)
	}
}

// /api/v1/hooks must return total + per_event even when no hooks are
// registered. Empty inventory → total=0, per_event={}, never null.
func TestHooksEndpoint_EmptyShape(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/hooks")
	if err != nil {
		t.Fatalf("get hooks: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if total, ok := payload["total"].(float64); !ok || total != 0 {
		t.Errorf("expected total=0 on fresh engine; got %v", payload["total"])
	}
	if _, ok := payload["per_event"]; !ok {
		t.Errorf("expected per_event field; got %#v", payload)
	}
}

// /api/v1/doctor must return a status field of "ok"|"warn"|"fail" plus
// pass/warn/fail counts in summary and a non-empty checks array. Pin
// these so monitoring code can rely on the shape across releases.
func TestDoctorEndpoint_ShapeAndStatus(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/doctor")
	if err != nil {
		t.Fatalf("get doctor: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	status, _ := payload["status"].(string)
	switch status {
	case "ok", "warn", "fail":
	default:
		t.Errorf("status must be ok|warn|fail; got %q", status)
	}
	checks, ok := payload["checks"].([]any)
	if !ok || len(checks) == 0 {
		t.Errorf("checks must be a non-empty array; got %#v", payload["checks"])
	}
	summary, ok := payload["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary must be an object; got %#v", payload["summary"])
	}
	for _, key := range []string{"pass", "warn", "fail"} {
		if _, ok := summary[key]; !ok {
			t.Errorf("summary missing %q", key)
		}
	}
}

func TestDoctorEndpoint_ZAIAdvisory(t *testing.T) {
	eng := newTestEngine(t)
	zai := eng.Config.Providers.Profiles["zai"]
	zai.Protocol = "anthropic"
	zai.BaseURL = "https://api.z.ai/api/anthropic"
	eng.Config.Providers.Profiles["zai"] = zai

	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/doctor")
	if err != nil {
		t.Fatalf("get doctor: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	var payload struct {
		Status string `json:"status"`
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Status != "warn" && payload.Status != "fail" {
		t.Fatalf("expected warn/fail status when ZAI advisory is present, got %q", payload.Status)
	}
	found := false
	for _, check := range payload.Checks {
		if check.Name == "provider.zai.advisory" && check.Status == "warn" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected provider.zai.advisory warn check, got %#v", payload.Checks)
	}
}

// /api/v1/config must redact every secret-shaped key. Plant a fake API
// key in the config and confirm it surfaces as "***REDACTED***" — never
// the literal value, no matter how the YAML round-trip lays it out.
func TestConfigEndpoint_RedactsSecrets(t *testing.T) {
	eng := newTestEngine(t)
	const sentinel = "sk-test-DO-NOT-LEAK-2026"
	prof := eng.Config.Providers.Profiles["anthropic"]
	prof.APIKey = sentinel
	eng.Config.Providers.Profiles["anthropic"] = prof

	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/config")
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	body, err := readAllString(resp)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if strings.Contains(body, sentinel) {
		t.Fatalf("config endpoint leaked api_key value into response")
	}
	if !strings.Contains(body, "***REDACTED***") {
		t.Errorf("expected redacted placeholder in body; got %q", body[:min(len(body), 200)])
	}
}

func TestScanEndpoint_RejectsParentTraversal(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/scan?path=../../../../../../../../etc")
	if err != nil {
		t.Fatalf("get scan: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 on parent traversal, got %d", resp.StatusCode)
	}
	body, err := readAllString(resp)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(body, "project root") {
		t.Fatalf("rejection message should explain the constraint; got %q", body)
	}
}

func readAllString(resp *http.Response) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			if err.Error() == "EOF" {
				return sb.String(), nil
			}
			return sb.String(), err
		}
	}
}
