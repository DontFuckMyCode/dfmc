// Remote HTTP client primitives used by `dfmc remote *` subcommands:
// endpoint probing, SSE chat stream consumption, JSON request helpers,
// generic key=value flag parsers, and codemap payload decoding.
// Extracted from cli_remote.go — no subcommand dispatcher code here,
// just the building blocks each subcommand stitches together.

package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

type remoteProbeResult struct {
	Endpoint   string `json:"endpoint"`
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code"`
	DurationMs int64  `json:"duration_ms"`
	Body       string `json:"body,omitempty"`
	Error      string `json:"error,omitempty"`
}

func parseEndpointList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return []string{"/healthz"}
	}
	return out
}

func probeRemoteEndpoint(client *http.Client, baseURL, endpoint, token string) remoteProbeResult {
	start := time.Now()
	res := remoteProbeResult{Endpoint: endpoint}

	url := strings.TrimRight(strings.TrimSpace(baseURL), "/") + endpoint
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		res.Error = err.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	if tok := strings.TrimSpace(token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := client.Do(req)
	if err != nil {
		res.Error = err.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	res.StatusCode = resp.StatusCode
	res.Body = strings.TrimSpace(string(body))
	res.OK = resp.StatusCode >= 200 && resp.StatusCode < 300
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}

type remoteChatEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta,omitempty"`
	Error string `json:"error,omitempty"`
}

func remoteAsk(baseURL, token, message string, timeout time.Duration, streamOutput bool) ([]remoteChatEvent, string, error) {
	payload, err := json.Marshal(map[string]string{"message": message})
	if err != nil {
		return nil, "", err
	}
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/api/v1/chat"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if tok := strings.TrimSpace(token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", fmt.Errorf("remote returned %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	events := make([]remoteChatEvent, 0, 64)
	var answer strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		ev, ok, err := parseSSEDataLine(scanner.Text())
		if err != nil {
			return events, answer.String(), err
		}
		if !ok {
			continue
		}
		events = append(events, ev)
		switch ev.Type {
		case "delta":
			answer.WriteString(ev.Delta)
			if streamOutput {
				fmt.Print(ev.Delta)
			}
		case "error":
			msg := strings.TrimSpace(ev.Error)
			if msg == "" {
				msg = "remote stream error"
			}
			return events, answer.String(), errors.New(msg)
		case "done":
			return events, answer.String(), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return events, answer.String(), err
	}
	return events, answer.String(), nil
}

func parseSSEDataLine(line string) (remoteChatEvent, bool, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return remoteChatEvent{}, false, nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" {
		return remoteChatEvent{}, false, nil
	}
	var ev remoteChatEvent
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		return remoteChatEvent{}, true, fmt.Errorf("invalid sse json: %w", err)
	}
	return ev, true, nil
}

func parseSSEJSONLine(line string) (map[string]any, bool, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return nil, false, nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" {
		return nil, false, nil
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		return nil, true, fmt.Errorf("invalid sse json: %w", err)
	}
	return out, true, nil
}

type multiStringFlag []string

func (m *multiStringFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiStringFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func parseKeyValueParams(items []string) (map[string]any, error) {
	out := map[string]any{}
	for _, raw := range items {
		parts := strings.SplitN(strings.TrimSpace(raw), "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid key=value: %s", raw)
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			return nil, fmt.Errorf("empty key in param: %s", raw)
		}
		val, err := parseConfigValue(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, err
		}
		out[key] = val
	}
	return out, nil
}

func parsePromptVars(items []string) (map[string]string, error) {
	out := map[string]string{}
	for _, raw := range items {
		parts := strings.SplitN(strings.TrimSpace(raw), "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid key=value: %s", raw)
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			return nil, fmt.Errorf("empty key in var: %s", raw)
		}
		out[key] = strings.TrimSpace(parts[1])
	}
	return out, nil
}

func remoteJSONRequest(method, endpoint, token string, payload any, timeout time.Duration) (map[string]any, int, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok := strings.TrimSpace(token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	out := map[string]any{}
	if len(strings.TrimSpace(string(rawBody))) > 0 {
		if err := json.Unmarshal(rawBody, &out); err != nil {
			return nil, resp.StatusCode, fmt.Errorf("invalid json response (%d): %s", resp.StatusCode, strings.TrimSpace(string(rawBody)))
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if msg, ok := out["error"]; ok {
			return out, resp.StatusCode, fmt.Errorf("remote returned %s: %v", resp.Status, msg)
		}
		return out, resp.StatusCode, fmt.Errorf("remote returned %s", resp.Status)
	}
	return out, resp.StatusCode, nil
}

func remoteCollectEvents(endpoint, token string, timeout time.Duration, maxEvents int) ([]map[string]any, error) {
	if maxEvents <= 0 {
		maxEvents = 100
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if tok := strings.TrimSpace(token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("remote returned %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	capHint := maxEvents
	if capHint > 32 {
		capHint = 32
	}
	events := make([]map[string]any, 0, capHint)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		ev, ok, err := parseSSEJSONLine(scanner.Text())
		if err != nil {
			return events, err
		}
		if !ok {
			continue
		}
		events = append(events, ev)
		if len(events) >= maxEvents {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return events, err
	}
	return events, nil
}

func compactJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

func decodeCodemapPayload(payload map[string]any) ([]codemap.Node, []codemap.Edge, error) {
	nodes := []codemap.Node{}
	edges := []codemap.Edge{}

	nodesRaw, ok := payload["nodes"]
	if !ok {
		return nodes, edges, fmt.Errorf("missing nodes field")
	}
	edgesRaw, ok := payload["edges"]
	if !ok {
		return nodes, edges, fmt.Errorf("missing edges field")
	}
	nb, err := json.Marshal(nodesRaw)
	if err != nil {
		return nodes, edges, err
	}
	if err := json.Unmarshal(nb, &nodes); err != nil {
		return nodes, edges, err
	}
	eb, err := json.Marshal(edgesRaw)
	if err != nil {
		return nodes, edges, err
	}
	if err := json.Unmarshal(eb, &edges); err != nil {
		return nodes, edges, err
	}
	return nodes, edges, nil
}

func remotePathEscape(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
