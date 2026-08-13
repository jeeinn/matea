//go:build ignore

// e2e-mock-hermes.go — minimal fake Hermes Runs API server for local E2E.
//
// Implements the contract consumed by internal/agents/backends/hermes:
//
//	POST /v1/runs        → {run_id, status:"started"}   (Bearer auth required)
//	GET  /v1/runs/{id}   → {status, output, session_id} (running until completed)
//
// Debug/introspection endpoints (no auth):
//
//	GET  /debug/submissions → JSON array of all POST /v1/runs bodies received
//	POST /debug/complete    → mark ALL running runs to complete on next poll
//	POST /debug/reset       → clear runs + submissions
//	GET  /health            → {"status":"ok"}
//
// Run behavior: a run stays "running" for MinPolls polls (default 2), then
// completes automatically. If HoldRuns is set (via /debug/hold?on=1), runs
// never complete until POST /debug/complete flips them — used by the Matea
// restart re-attach E2E (kill gateway mid-poll, restart, flip, observe
// exactly one submission per task).
//
// Usage: go run scripts/common/e2e-mock-hermes.go [-addr 127.0.0.1:9090] [-token hermes-e2e-token] [-log data/logs-e2e/hermes-requests.jsonl]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type run struct {
	ID        string `json:"run_id"`
	SessionID string `json:"session_id,omitempty"`
	Status    string `json:"status"`
	Output    string `json:"output,omitempty"`
	Polls     int    `json:"polls"`
	Complete  bool   `json:"complete"` // flipped by /debug/complete
}

type server struct {
	mu          sync.Mutex
	runs        map[string]*run
	submissions []map[string]any
	hold        bool
	minPolls    int
	token       string
	logFile     string
	seq         int
}

func (s *server) appendLog(kind string, v any) {
	if s.logFile == "" {
		return
	}
	f, err := os.OpenFile(s.logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("warn: open log: %v", err)
		return
	}
	defer f.Close()
	rec := map[string]any{"at": time.Now().Format(time.RFC3339), "kind": kind, "payload": v}
	data, _ := json.Marshal(rec)
	f.Write(append(data, '\n'))
}

func (s *server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.token != "" && r.Header.Get("Authorization") != "Bearer "+s.token {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.seq++
	id := fmt.Sprintf("run_%04d", s.seq)
	sess, _ := body["session_id"].(string)
	if sess == "" {
		sess = "ses_" + id
	}
	s.runs[id] = &run{ID: id, SessionID: sess, Status: "running"}
	bodyCopy := body
	bodyCopy["_run_id"] = id
	s.submissions = append(s.submissions, bodyCopy)
	s.mu.Unlock()

	s.appendLog("submit", bodyCopy)
	log.Printf("submit %s session=%s input_len=%v", id, sess, len(fmt.Sprint(body["input"])))
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"run_id":%q,"status":"started"}`, id)
}

func (s *server) handleRunPoll(w http.ResponseWriter, r *http.Request, id string) {
	if s.token != "" && r.Header.Get("Authorization") != "Bearer "+s.token {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	ru, ok := s.runs[id]
	if ok {
		ru.Polls++
		if (ru.Polls >= s.minPolls && !s.hold) || ru.Complete {
			ru.Status = "completed"
			if ru.Output == "" {
				ru.Output = fmt.Sprintf("FAKE-HERMES-OUTPUT for %s (session %s): 分析完成，证据链见 input。", id, ru.SessionID)
			}
		}
	}
	resp := map[string]any{}
	if ok {
		resp = map[string]any{"status": ru.Status, "output": ru.Output, "session_id": ru.SessionID}
	}
	s.mu.Unlock()
	if !ok {
		http.Error(w, `{"error":"run not found"}`, http.StatusNotFound)
		return
	}
	s.appendLog("poll", map[string]any{"run_id": id, "status": resp["status"]})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *server) mux() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	m.HandleFunc("/v1/runs", s.handleRuns)
	m.HandleFunc("/v1/runs/", func(w http.ResponseWriter, r *http.Request) {
		s.handleRunPoll(w, r, strings.TrimPrefix(r.URL.Path, "/v1/runs/"))
	})
	m.HandleFunc("/debug/submissions", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.submissions)
	})
	m.HandleFunc("/debug/complete", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		n := 0
		for _, ru := range s.runs {
			if ru.Status == "running" {
				ru.Complete = true
				n++
			}
		}
		s.mu.Unlock()
		log.Printf("debug/complete: flipped %d runs", n)
		fmt.Fprintf(w, `{"flipped":%d}`, n)
	})
	m.HandleFunc("/debug/hold", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hold = r.URL.Query().Get("on") == "1"
		log.Printf("debug/hold: hold=%v", s.hold)
		s.mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	})
	m.HandleFunc("/debug/reset", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.runs = map[string]*run{}
		s.submissions = nil
		s.mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	})
	return m
}

func main() {
	addr := flag.String("addr", "127.0.0.1:9090", "listen address")
	token := flag.String("token", "hermes-e2e-token", "required Bearer token (empty = no auth)")
	minPolls := flag.Int("min-polls", 2, "polls before a run auto-completes (unless held)")
	logFile := flag.String("log", "data/logs-e2e/hermes-requests.jsonl", "request log file")
	flag.Parse()

	s := &server{
		runs:     map[string]*run{},
		minPolls: *minPolls,
		token:    *token,
		logFile:  *logFile,
	}
	log.Printf("fake-hermes listening on %s (token=%v minPolls=%d)", *addr, *token != "", *minPolls)
	log.Fatal(http.ListenAndServe(*addr, s.mux()))
}
