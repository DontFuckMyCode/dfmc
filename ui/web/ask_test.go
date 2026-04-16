package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAskEndpointNonRace: plain POST /api/v1/ask must route through
// engine.Ask and return {answer, mode:"single"}. Uses the offline
// provider (always registered) so the test has no upstream dependency.
func TestAskEndpointNonRace(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := bytes.NewBufferString(`{"message":"summarize this"}`)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/ask", body)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if mode, _ := payload["mode"].(string); mode != "single" {
		t.Fatalf("mode=%q, want single (payload=%+v)", mode, payload)
	}
	if answer, _ := payload["answer"].(string); strings.TrimSpace(answer) == "" {
		t.Fatalf("empty answer: %+v", payload)
	}
}

// TestAskEndpointRaceMode: setting race=true + race_providers=[offline]
// must return {winner, mode:"race"}. Offline is guaranteed registered, so
// this test doesn't need live upstream keys.
func TestAskEndpointRaceMode(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := bytes.NewBufferString(`{"message":"summarize this","race":true,"race_providers":["offline"]}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/ask", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if mode, _ := payload["mode"].(string); mode != "race" {
		t.Fatalf("mode=%q, want race (payload=%+v)", mode, payload)
	}
	if winner, _ := payload["winner"].(string); winner != "offline" {
		t.Fatalf("winner=%q, want offline (payload=%+v)", winner, payload)
	}
}

// TestAskEndpointRejectsEmpty: both race and non-race paths must refuse
// an empty message before any provider is dispatched.
func TestAskEndpointRejectsEmpty(t *testing.T) {
	eng := newTestEngine(t)
	srv := New(eng, "127.0.0.1", 0)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	cases := []string{
		`{"message":""}`,
		`{"message":"   "}`,
		`{"message":"","race":true}`,
	}
	for _, c := range cases {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/ask", bytes.NewBufferString(c))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do %q: %v", c, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %q: status=%d, want 400", c, resp.StatusCode)
		}
	}
}
