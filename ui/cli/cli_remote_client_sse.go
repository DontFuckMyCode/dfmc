package cli

// cli_remote_client_sse.go — server-sent-event consumers for the
// `dfmc remote *` subcommands: remoteAsk (POST /api/v1/chat with
// streamed delta/error/done events) and remoteCollectEvents (GET an
// SSE endpoint into a bounded slice of decoded JSON frames). The two
// SSE line-parsers (parseSSEDataLine / parseSSEJSONLine) live here
// too so the streaming surface is one file.
//
// Sibling of cli_remote_client.go which keeps the non-streaming
// helpers (endpoint probe, JSON request, flag parsers, codemap
// payload decoder, path escape).

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

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
