// Chat/streaming handlers and shared response helpers for the web API.
// Extracted from server.go to keep the construction/wiring lean. Ask/Chat/
// WebSocket live together because they all drive provider streaming; writeSSE
// and writeJSON are shared response primitives used by every other handler.

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/provider"
)

// handleAsk answers a single-turn prompt. Unlike /api/v1/chat, which
// streams and stays in the agent tool loop, this endpoint is one-shot: it
// returns when the first provider reply comes back. When req.Race is true,
// the router fans out to every candidate concurrently and the winner's
// name is included in the response.
func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	var req AskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "message is required"})
		return
	}
	if req.Race {
		answer, winner, err := s.engine.AskRaced(r.Context(), msg, req.RaceProviders)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"answer":     answer,
			"winner":     winner,
			"candidates": req.RaceProviders,
			"mode":       "race",
		})
		return
	}
	answer, err := s.engine.Ask(r.Context(), msg)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"answer": answer,
		"mode":   "single",
	})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "streaming not supported"})
		return
	}
	clearStreamingWriteDeadline(w)

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "message is required"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	stream, err := s.engine.StreamAsk(r.Context(), req.Message)
	if err != nil {
		writeSSE(w, flusher, map[string]any{
			"type":  "error",
			"error": err.Error(),
		})
		return
	}

	for ev := range stream {
		switch ev.Type {
		case provider.StreamDelta:
			writeSSE(w, flusher, map[string]any{
				"type":  "delta",
				"delta": ev.Delta,
			})
		case provider.StreamError:
			writeSSE(w, flusher, map[string]any{
				"type":  "error",
				"error": ev.Err.Error(),
			})
			return
		case provider.StreamDone:
			writeSSE(w, flusher, map[string]any{
				"type": "done",
				"ts":   time.Now().UTC().Format(time.RFC3339),
			})
			return
		}
	}
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "streaming not supported"})
		return
	}
	clearStreamingWriteDeadline(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	eventType := "*"
	if t := strings.TrimSpace(r.URL.Query().Get("type")); t != "" {
		eventType = t
	}
	ch := make(chan engine.Event, 128)
	unsubscribe := s.engine.EventBus.SubscribeFunc(eventType, func(ev engine.Event) {
		select {
		case ch <- ev:
		default:
		}
	})
	defer unsubscribe()

	writeSSE(w, flusher, map[string]any{
		"type": "connected",
		"ts":   time.Now().UTC().Format(time.RFC3339),
	})

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			writeSSE(w, flusher, map[string]any{
				"type":    "event",
				"event":   ev.Type,
				"source":  ev.Source,
				"payload": ev.Payload,
				"ts":      ev.Timestamp.UTC().Format(time.RFC3339),
			})
		case <-ticker.C:
			writeSSE(w, flusher, map[string]any{
				"type": "ping",
				"ts":   time.Now().UTC().Format(time.RFC3339),
			})
		}
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		// SSE write failure is non-fatal (connection may have dropped);
		// flusher already flushed, so we log and continue.
		fmt.Fprintf(os.Stderr, "dfmc: writeSSE error: %v\n", err)
		return
	}
	flusher.Flush()
}

// writeSSEWithDeadline writes an SSE frame with a per-chunk deadline to
// prevent a slow-loris reader from pinning the handler goroutine forever.
// Returns false if the write failed (deadline exceeded or connection dead).
func writeSSEWithDeadline(w http.ResponseWriter, flusher http.Flusher, payload any) bool {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Second))
	data, _ := json.Marshal(payload)
	_, err := fmt.Fprintf(w, "data: %s\n\n", data)
	if err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func clearStreamingWriteDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		fmt.Fprintf(os.Stderr, "dfmc: writeJSON encode error: %v\n", err)
	}
}
