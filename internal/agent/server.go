package agent

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

// HTTP handler methods for Agent

func (a *Agent) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": "ok"})
}

func (a *Agent) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(a.containerState)
}

func (a *Agent) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "Method not allowed. Use PUT."})
		slog.Warn("Method not allowed", "path", r.URL.Path, "method", r.Method)
		return
	}

	if a.Command == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "no command configured"})
		return
	}
	if a.isProcessRunning() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "command already running"})
		return
	}

	// Load env vars from file and generate Wireproxy config file
	if err := godotenv.Load("/etc/virtualpod/environment"); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "failed to load environment file: " + err.Error()})
		slog.Error("Failed to load environment file", "path", "/etc/virtualpod/environment", "error", err)
		return
	}

	// Start promtail before wireproxy if enabled
	if a.EnablePromtail {
		if err := a.startPromtail(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "failed to start promtail: " + err.Error()})
			return
		}
	}

	// Start wireproxy before the main command
	if a.EnableProxy {
		if err := a.startWireproxy(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "failed to start wireproxy: " + err.Error()})
			return
		}
	}

	if err := a.startChildProcess(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "failed to start command: " + err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": "execution started"})
}

func (a *Agent) handleSigterm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "Method not allowed. Use POST."})
		slog.Warn("Method not allowed", "path", r.URL.Path, "method", r.Method)
		return
	}
	go a.handleSigtermSignal()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": "Termination sequence initiated"})
	slog.Info("Termination sequence initiated")
}

func (a *Agent) handlePushFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "Method not allowed. Use POST."})
		slog.Warn("Method not allowed", "path", r.URL.Path, "method", r.Method)
		return
	}
	type reqBody struct {
		FilePath string `json:"filepath"`
		Data     string `json:"data"`
	}
	var body reqBody
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "invalid JSON: " + err.Error()})
		slog.Warn("Invalid JSON", "error", err)
		return
	}
	if body.FilePath == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "filepath is required"})
		slog.Warn("Missing filepath in request body")
		return
	}
	parent := filepath.Dir(body.FilePath)
	if parent != "." && parent != "" {
		if err := os.MkdirAll(parent, 0755); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "failed to create parent directory: " + err.Error()})
			slog.Error("Failed to create parent directory", "path", parent, "error", err)
			return
		}
	}
	f, err := os.OpenFile(body.FilePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "failed to open file for write: " + err.Error()})
		slog.Error("Failed to open file for write", "path", body.FilePath, "error", err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(body.Data); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "failed to write file: " + err.Error()})
		slog.Error("Failed to write file", "path", body.FilePath, "error", err)
		return
	}
	if err := f.Sync(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "failed to sync file: " + err.Error()})
		slog.Error("Failed to sync file", "path", body.FilePath, "error", err)
		return
	}
	if parent != "." {
		dir, err := os.Open(parent)
		if err == nil {
			_ = dir.Sync()
			_ = dir.Close()
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  http.StatusCreated,
		"bytes":   len(body.Data),
		"message": "file saved and synced",
	})
}
