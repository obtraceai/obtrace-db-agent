package main

import (
	"encoding/json"
	"net/http"
	"time"
)

type HealthServer struct {
	orchestrator *ResilientOrchestrator
	startTime    time.Time
	lastSendOK   time.Time
	lastSendErr  string
	port         string
}

func NewHealthServer(orch *ResilientOrchestrator, port string) *HealthServer {
	return &HealthServer{
		orchestrator: orch,
		startTime:    time.Now(),
		port:         port,
	}
}

func (h *HealthServer) RecordSend(err error) {
	if err == nil {
		h.lastSendOK = time.Now()
		h.lastSendErr = ""
	} else {
		h.lastSendErr = err.Error()
	}
}

func (h *HealthServer) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/ready", h.handleReady)

	server := &http.Server{
		Addr:         ":" + h.port,
		Handler:      mux,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	}

	go server.ListenAndServe()
}

func (h *HealthServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	statuses := h.orchestrator.Status()

	healthy := true
	for _, s := range statuses {
		if s.Status == "open" {
			healthy = false
			break
		}
	}

	resp := map[string]interface{}{
		"status":     "ok",
		"uptime_s":   time.Since(h.startTime).Seconds(),
		"collectors": statuses,
	}

	if !h.lastSendOK.IsZero() {
		resp["last_send_ok"] = h.lastSendOK.Format(time.RFC3339)
	}
	if h.lastSendErr != "" {
		resp["last_send_error"] = h.lastSendErr
	}

	if !healthy {
		resp["status"] = "degraded"
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *HealthServer) handleReady(w http.ResponseWriter, r *http.Request) {
	if h.lastSendOK.IsZero() && time.Since(h.startTime) > 30*time.Second {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"ready":false,"reason":"no successful send yet"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ready":true}`))
}
