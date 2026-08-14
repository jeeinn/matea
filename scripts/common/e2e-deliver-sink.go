//go:build ignore

// e2e-deliver-sink.go — receive-end for Matea's outbound deliver events (E2E).
//
//	POST /event   → append raw JSON body to the log file, return 200
//	GET  /events  → JSON array of every event body received so far
//	POST /debug/reset → clear
//	GET  /health  → {"status":"ok"}
//
// Usage: go run scripts/common/e2e-deliver-sink.go [-addr 127.0.0.1:9095] [-log data/logs-e2e/deliver-events.jsonl]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

type sink struct {
	mu      sync.Mutex
	events  []map[string]any
	logFile string
}

func (s *sink) handleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	body["_received_at"] = time.Now().Format(time.RFC3339)
	s.events = append(s.events, body)
	s.mu.Unlock()

	if s.logFile != "" {
		if f, err := os.OpenFile(s.logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			data, _ := json.Marshal(body)
			f.Write(append(data, '\n'))
			f.Close()
		}
	}
	log.Printf("event: %s action=%v repo=%v issue=%v pr=%v", body["event"], body["action"], body["repo"], body["issue_id"], body["pr_id"])
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func main() {
	addr := flag.String("addr", "127.0.0.1:9095", "listen address")
	logFile := flag.String("log", "data/logs-e2e/deliver-events.jsonl", "event log file")
	flag.Parse()

	s := &sink{logFile: *logFile}
	m := http.NewServeMux()
	m.HandleFunc("/event", s.handleEvent)
	m.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if s.events == nil {
			w.Write([]byte(`[]`))
			return
		}
		json.NewEncoder(w).Encode(s.events)
	})
	m.HandleFunc("/debug/reset", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.events = nil
		s.mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	})
	m.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	fmt.Printf("deliver-sink listening on %s\n", *addr)
	log.Fatal(http.ListenAndServe(*addr, m))
}
