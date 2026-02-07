// Package health provides an HTTP health check endpoint for dbstash.
package health

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/viperadnan/dbstash/internal/logger"
)

// Status represents the health check response.
type Status struct {
	Status     string `json:"status"`
	Engine     string `json:"engine"`
	LastBackup string `json:"last_backup"`
	LastStatus string `json:"last_status"`
}

// Tracker tracks the last backup time and status in a thread-safe manner.
type Tracker struct {
	mu         sync.RWMutex
	engine     string
	lastBackup time.Time
	lastStatus string
}

// NewTracker creates a new health tracker for the given engine.
func NewTracker(engine string) *Tracker {
	return &Tracker{
		engine:     engine,
		lastStatus: "pending",
	}
}

// Update records the result of a backup run.
func (t *Tracker) Update(status string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastBackup = time.Now()
	t.lastStatus = status
}

// GetStatus returns the current health status.
func (t *Tracker) GetStatus() Status {
	t.mu.RLock()
	defer t.mu.RUnlock()

	lastBackup := ""
	if !t.lastBackup.IsZero() {
		lastBackup = t.lastBackup.Format(time.RFC3339)
	}

	return Status{
		Status:     "healthy",
		Engine:     t.engine,
		LastBackup: lastBackup,
		LastStatus: t.lastStatus,
	}
}

// StartServer starts the health check HTTP server on the given address.
// It blocks until the server is shut down.
func StartServer(addr string, tracker *Tracker) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(tracker.GetStatus())
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Error().Err(err).Str("addr", addr).Msg("health server error")
		}
	}()

	logger.Log.Info().Str("addr", addr).Msg("health check server started")
	return server
}
