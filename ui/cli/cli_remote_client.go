// Remote HTTP client primitives used by `dfmc remote *` subcommands:
// endpoint probing, SSE chat stream consumption, JSON request helpers,
// generic key=value flag parsers, and codemap payload decoding.
// Extracted from cli_remote.go — no subcommand dispatcher code here,
// just the building blocks each subcommand stitches together.

package cli

import (
	"bytes"
	"encoding/json"
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

// remoteChatEvent + remoteAsk + parseSSEDataLine + parseSSEJSONLine +
// remoteCollectEvents live in cli_remote_client_sse.go.

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
