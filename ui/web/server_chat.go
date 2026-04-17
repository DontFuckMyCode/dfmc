// Chat/streaming handlers and shared response helpers for the web API.
// Extracted from server.go to keep the construction/wiring lean. Ask/Chat/
// WebSocket live together because they all drive provider streaming; writeSSE
// and writeJSON are shared response primitives used by every other handler.

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

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
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	eventType := "*"
	if t := strings.TrimSpace(r.URL.Query().Get("type")); t != "" {
		eventType = t
	}
	ch := s.engine.EventBus.Subscribe(eventType)
	defer s.engine.EventBus.Unsubscribe(eventType, ch)

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
		case ev, ok := <-ch:
			if !ok {
				return
			}
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
	data, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}
