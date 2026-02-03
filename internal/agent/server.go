package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// HTTP handler methods for Agent

// respondJSON writes a JSON response with the given status code and payload
func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (a *Agent) handleHealthz(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{"message": "ok"})
}

func (a *Agent) handleStatus(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, a.containerState)
}

func (a *Agent) handleWireproxyRestart(w http.ResponseWriter, r *http.Request) {
	if a.EnableProxy {
		if err := a.startWireproxy(); err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]any{"message": "failed to start wireproxy: " + err.Error()})
			return
		}
	}

	respondJSON(w, http.StatusOK, map[string]any{"message": "ok"})
}

func (a *Agent) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]any{"message": "Method not allowed. Use PUT."})
		slog.Warn("Method not allowed", "path", r.URL.Path, "method", r.Method)
		return
	}

	if a.Command == "" && len(a.Argv) == 0 {
		respondJSON(w, http.StatusBadRequest, map[string]any{"message": "no command configured"})
		return
	}
	if a.isProcessRunning() {
		respondJSON(w, http.StatusConflict, map[string]any{"message": "command already running"})
		return
	}

	// Load env vars from file and generate Wireproxy config file
	if err := godotenv.Load("/etc/virtualpod/environment"); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{"message": "failed to load environment file: " + err.Error()})
		slog.Error("Failed to load environment file", "path", "/etc/virtualpod/environment", "error", err)
		return
	}

	// Start promtail before wireproxy if enabled
	if a.EnablePromtail {
		if err := a.startPromtail(); err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]any{"message": "failed to start promtail: " + err.Error()})
			return
		}
	}

	// Start wireproxy before the main command
	//if a.EnableProxy {
	//	if err := a.startWireproxy(); err != nil {
	//		respondJSON(w, http.StatusInternalServerError, map[string]any{"message": "failed to start wireproxy: " + err.Error()})
	//		return
	//	}
	//}

	if err := a.startChildProcess(); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{"message": "failed to start command: " + err.Error()})
		return
	}
	respondJSON(w, http.StatusAccepted, map[string]any{"message": "execution started"})
}

func (a *Agent) handleSigterm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]any{"message": "Method not allowed. Use POST."})
		slog.Warn("Method not allowed", "path", r.URL.Path, "method", r.Method)
		return
	}
	go a.handleSigtermSignal()
	respondJSON(w, http.StatusAccepted, map[string]any{"message": "Termination sequence initiated"})
	slog.Info("Termination sequence initiated")
}

func (a *Agent) handlePushFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]any{"message": "Method not allowed. Use POST."})
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
		respondJSON(w, http.StatusBadRequest, map[string]any{"message": "invalid JSON: " + err.Error()})
		slog.Warn("Invalid JSON", "error", err)
		return
	}
	if body.FilePath == "" {
		respondJSON(w, http.StatusBadRequest, map[string]any{"message": "filepath is required"})
		slog.Warn("Missing filepath in request body")
		return
	}
	parent := filepath.Dir(body.FilePath)
	if parent != "." && parent != "" {
		if err := os.MkdirAll(parent, 0755); err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]any{"message": "failed to create parent directory: " + err.Error()})
			slog.Error("Failed to create parent directory", "path", parent, "error", err)
			return
		}
	}
	f, err := os.OpenFile(body.FilePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{"message": "failed to open file for write: " + err.Error()})
		slog.Error("Failed to open file for write", "path", body.FilePath, "error", err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(body.Data); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{"message": "failed to write file: " + err.Error()})
		slog.Error("Failed to write file", "path", body.FilePath, "error", err)
		return
	}
	if err := f.Sync(); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{"message": "failed to sync file: " + err.Error()})
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
	respondJSON(w, http.StatusCreated, map[string]any{
		"status":  http.StatusCreated,
		"bytes":   len(body.Data),
		"message": "file saved and synced",
	})
}

func (a *Agent) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]any{"message": "Method not allowed. Use GET."})
		slog.Warn("Method not allowed", "path", r.URL.Path, "method", r.Method)
		return
	}

	query := r.URL.Query()
	follow := query.Get("follow") == "true"
	tailLines := query.Get("tail")

	logPath := "/var/log/container/main.log"
	
	// Check if log file exists
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		http.Error(w, "Log file not found", http.StatusNotFound)
		return
	}

	file, err := os.Open(logPath)
	if err != nil {
		http.Error(w, "Failed to open log file: "+err.Error(), http.StatusInternalServerError)
		slog.Error("Failed to open log file", "path", logPath, "error", err)
		return
	}
	defer file.Close()

	// Set headers for streaming
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)

	// If tail is specified, read from the end
	if tailLines != "" {
		var lines int
		if _, err := fmt.Sscanf(tailLines, "%d", &lines); err == nil && lines > 0 {
			if err := tailFile(file, w, lines); err != nil {
				slog.Error("Failed to tail log file", "error", err)
				return
			}
		}
	} else {
		// Read entire file from beginning
		if _, err := file.Seek(0, 0); err != nil {
			slog.Error("Failed to seek log file", "error", err)
			return
		}
		if _, err := file.WriteTo(w); err != nil {
			slog.Error("Failed to write log file", "error", err)
			return
		}
	}

	// If follow is enabled, keep streaming new logs
	if follow {
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		// Get current position
		pos, err := file.Seek(0, os.SEEK_CUR)
		if err != nil {
			slog.Error("Failed to get current position", "error", err)
			return
		}

		// Poll for new content
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				// Check if file has grown
				stat, err := file.Stat()
				if err != nil {
					slog.Error("Failed to stat log file", "error", err)
					return
				}

				if stat.Size() > pos {
					// Read new content
					buf := make([]byte, stat.Size()-pos)
					n, err := file.Read(buf)
					if err != nil && err != os.ErrClosed {
						slog.Error("Failed to read new log content", "error", err)
						return
					}
					if n > 0 {
						if _, err := w.Write(buf[:n]); err != nil {
							return
						}
						if flusher, ok := w.(http.Flusher); ok {
							flusher.Flush()
						}
						pos += int64(n)
					}
				}
			}
		}
	}
}

// tailFile reads the last n lines from a file and writes them to w
func tailFile(file *os.File, w http.ResponseWriter, n int) error {
	const bufSize = 4096
	stat, err := file.Stat()
	if err != nil {
		return err
	}

	size := stat.Size()
	if size == 0 {
		return nil
	}

	// Read file in chunks from the end to find n lines
	var lines []string
	buf := make([]byte, bufSize)
	offset := size

	for offset > 0 && len(lines) < n {
		readSize := int64(bufSize)
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize

		if _, err := file.Seek(offset, 0); err != nil {
			return err
		}

		nr, err := file.Read(buf[:readSize])
		if err != nil {
			return err
		}

		// Process buffer in reverse to collect lines
		chunk := string(buf[:nr])
		chunkLines := strings.Split(chunk, "\n")

		// Prepend lines (in reverse order)
		for i := len(chunkLines) - 1; i >= 0; i-- {
			if len(lines) >= n {
				break
			}
			if chunkLines[i] != "" || i < len(chunkLines)-1 {
				lines = append([]string{chunkLines[i]}, lines...)
			}
		}
	}

	// Keep only the last n lines
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	// Write the lines
	for _, line := range lines {
		if _, err := w.Write([]byte(line + "\n")); err != nil {
			return err
		}
	}

	return nil
}
