package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	PR_SET_CHILD_SUBREAPER = 36
	SYS_PRCTL              = 157 // syscall number for prctl
)

// Agent represents the container agent that manages a single child process
type Agent struct {
	Command                       string
	Port                          int
	AuthToken                     string
	EnablePromtail                bool
	EnableProxy                   bool
	server                        *http.Server
	mux                           *http.ServeMux
	cmd                           *exec.Cmd
	env                           []string
	pid                           int
	startTime                     time.Time
	containerState                v1.ContainerState
	stateMutex                    sync.RWMutex
	terminatedAt                  time.Time
	terminationGracePeriodSeconds int
	terminationMutex              sync.Mutex

	// wireproxy management
	wireCmd *exec.Cmd
	wirePid int

	// promtail management
	promtailCmd *exec.Cmd
	promtailPid int
}

// We're using v1.ContainerState from k8s.io/api/core/v1 instead of defining our own StatusResponse struct

func NewAgent(port int, command string) *Agent {
	agent := &Agent{
		Command: command,
		Port:    port,
		mux:     http.NewServeMux(),
		containerState: v1.ContainerState{
			Waiting: &v1.ContainerStateWaiting{
				Reason:  "ContainerCreating",
				Message: "Container is being created",
			},
		},
		terminationGracePeriodSeconds: 30, // Default to 30 seconds
	}

	// Healthz endpoint
	agent.mux.HandleFunc("/healthz", agent.handleHealthz)

	// Status endpoint (debugging state)
	agent.mux.HandleFunc("/status", agent.handleStatus)

	// Run endpoint to start the command on demand
	agent.mux.HandleFunc("/run", agent.handleRun)

	// Run endpoint to start the command on demand
	agent.mux.HandleFunc("/restart_wireproxy", agent.handleWireproxyRestart)

	// SIGTERM endpoint
	agent.mux.HandleFunc("/sigterm", agent.handleSigterm)

	// Load ConfigMap endpoint
	agent.mux.HandleFunc("/push_file", agent.handlePushFile)

	// Initialize the server
	addr := fmt.Sprintf("0.0.0.0:%d", agent.Port)
	agent.server = &http.Server{
		Addr:    addr,
		Handler: agent.authMiddleware(agent.mux),
	}

	return agent
}

// authMiddleware enforces Bearer token auth if AuthToken is set. If no token is configured, it passes through.
func (a *Agent) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.AuthToken == "" {
			// No auth required
			next.ServeHTTP(w, r)
			return
		}
		authz := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(authz, prefix) || strings.TrimSpace(strings.TrimPrefix(authz, prefix)) != a.AuthToken {
			slog.Warn("Unauthorized request", "path", r.URL.Path, "remote", r.RemoteAddr)
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": http.StatusUnauthorized, "message": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func readOomKill(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}

	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		var key string
		var val uint64
		fmt.Sscanf(s.Text(), "%s %d", &key, &val)
		if key == "oom_kill" {
			return val, nil
		}
	}

	return 0, nil
}

func (a *Agent) updateContainerState(state v1.ContainerState) {
	a.stateMutex.Lock()
	defer a.stateMutex.Unlock()
	a.containerState = state
}

// isProcessRunning checks if the child process is currently running
func (a *Agent) isProcessRunning() bool {
	if a.pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(a.pid)
	if err != nil || proc == nil {
		return false
	}
	// Signal 0 checks if process exists and we can signal it
	return proc.Signal(syscall.Signal(0)) == nil
}

// isPidAlive checks if a PID is alive
func isPidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil || proc == nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// startWireproxy renders /root/wireproxy.conf from /root/wireproxy.tpl with env vars and starts wireproxy
func (a *Agent) startWireproxy() error {
	// If already started and alive, do nothing
	if a.wirePid > 0 && isPidAlive(a.wirePid) {
		if proc, err := os.FindProcess(a.wirePid); err == nil {
			_ = proc.Kill()
		}
	}

	// Check if wireproxy.keys exists and load environment variables
	if keysData, err := os.ReadFile("/etc/virtualpod/wireproxy.keys"); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(keysData)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			if err := os.Setenv(key, value); err != nil {
				slog.Warn("Failed to set environment variable", "key", key, "error", err)
			}
		}
	}

	// 1) Render config
	const tplPath = "/etc/virtualpod/wireproxy.tpl"
	const confPath = "/etc/virtualpod/wireproxy.conf"
	data, err := os.ReadFile(tplPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", tplPath, err)
	}
	rendered := os.ExpandEnv(string(data))
	if err := os.WriteFile(confPath, []byte(rendered), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", confPath, err)
	}

	// 2) Start wireproxy
	cmd := exec.Command("/usr/bin/wireproxy", "-c", confPath)

	// Separate logging channel for wireproxy
	stdoutWriter, stderrWriter, logErr := SetupLogging("wireproxy", "wireproxy")
	if logErr != nil {
		return fmt.Errorf("failed to set up logging for wireproxy: %w", logErr)
	}
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	// Run as root with its own process group, do not set credentials (keeps root)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start wireproxy: %w", err)
	}
	a.wireCmd = cmd
	a.wirePid = cmd.Process.Pid
	slog.Info("Started wireproxy", "pid", a.wirePid)

	// Optionally, we can not wait; fire-and-forget
	go func() {
		_ = cmd.Wait()
	}()

	return nil
}

// startPromtail starts the promtail agent with provided config and discards output
func (a *Agent) startPromtail() error {
	if a.promtailPid > 0 && isPidAlive(a.promtailPid) {
		return nil
	}

	cmd := exec.Command("/usr/bin/promtail", "-config.file=/etc/virtualpod/promtail.yaml")

	// Discard all output as requested
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", os.DevNull, err)
	}
	cmd.Stdout = devnull
	cmd.Stderr = devnull

	// own process group like wireproxy
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start promtail: %w", err)
	}
	a.promtailCmd = cmd
	a.promtailPid = cmd.Process.Pid
	slog.Info("Started promtail", "pid", a.promtailPid)

	go func() {
		_ = cmd.Wait()
	}()

	return nil
}

func applyEnvOverrides(env []string) []string {
	overrides := make(map[string]string)
	normal := make(map[string]string)

	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := parts[0], parts[1]

		if strings.HasPrefix(key, "VASTAI_OW_") {
			// store override under the stripped key
			target := strings.TrimPrefix(key, "VASTAI_OW_")
			overrides[target] = val
		} else {
			normal[key] = val
		}
	}

	// apply overrides (last one wins)
	for k, v := range overrides {
		normal[k] = v
	}

	// rebuild []string
	out := make([]string, 0, len(normal))
	for k, v := range normal {
		out = append(out, k+"="+v)
	}
	return out
}

// startChildProcess starts the child process specified by the Command field
func (a *Agent) startChildProcess() error {
	if a.Command == "" {
		return fmt.Errorf("no command specified")
	}

	// Parse the command string into command and arguments
	parts := strings.Fields(a.Command)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	cmdPath := parts[0]
	var args []string
	if len(parts) > 1 {
		args = parts[1:]
	}

	// Create the command
	a.cmd = exec.Command(cmdPath, args...)
	a.cmd.Env = applyEnvOverrides(os.Environ())

	// Set process group ID to make it easier to send signals to the whole group
	// Also set the user and group ID to run as user gnome (uid 1000, gid 1000)
	a.cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // This is equivalent to setpgid(0,0)
		//Credential: &syscall.Credential{
		//	Uid: 1000,
		//	Gid: 1000,
		//},
	}

	// Set up logging for stdout and stderr using the logging package
	stdoutWriter, stderrWriter, err := SetupLogging("main", "main")
	if err != nil {
		return fmt.Errorf("failed to set up logging: %w", err)
	}

	// Set the command's stdout and stderr to the writers returned by SetupLogging
	a.cmd.Stdout = stdoutWriter
	a.cmd.Stderr = stderrWriter

	// Set up process reaper by calling prctl(PR_SET_CHILD_SUBREAPER, 1)
	// This is Linux-specific and requires a direct syscall
	_, _, errno := syscall.RawSyscall6(SYS_PRCTL, PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("failed to set child subreaper: %v", errno)
	}

	// Start the process
	if err := a.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	// Record PID and start time
	a.pid = a.cmd.Process.Pid
	a.startTime = time.Now()

	// Update state to running
	a.updateContainerState(v1.ContainerState{
		Running: &v1.ContainerStateRunning{
			StartedAt: metav1.NewTime(a.startTime),
		},
	})

	slog.Info("Started child process", "pid", a.pid, "command", a.Command)

	// Start a goroutine to wait for the process to complete
	go func() {
		waitErr := a.cmd.Wait()
		// a.stateMutex.Lock()
		// defer a.stateMutex.Unlock()

		var exitErr *exec.ExitError
		a.terminatedAt = time.Now()
		ws := a.cmd.ProcessState.Sys().(syscall.WaitStatus)
		// sig := ws.Signal()          // zero if none
		exitCode := ws.ExitStatus() // 0-255 or -1 when signalled

		if waitErr == nil {
			exitCode = 0
		} else if errors.As(waitErr, &exitErr) {
			ws := exitErr.Sys().(syscall.WaitStatus)
			sig := ws.Signal()
			if sig > 0 {
				exitCode = 128 + int(sig) // use Kubernetes-style code
			} else {
				exitCode = ws.ExitStatus() // already 0-255
			}
		} else {
			exitCode = 1 // "unknown failure"
		}

		// TODO: Cannot create separate CGROUP in unprivileged container; add periodic OOM counter update during run
		oomKilled := false
		//cgroupOomCounter, err := readOomKill("/sys/fs/cgroup/memory.events")
		//if sig == 9 && cgroupOomCounter > 0 {
		//	oomKilled = true
		//}

		var reason string
		if oomKilled {
			reason = "OOMKilled"
		} else if exitCode > 0 {
			reason = "Error" // or "Killed"
		} else {
			reason = "Completed"
		}

		a.updateContainerState(v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				ExitCode:   int32(exitCode),
				Reason:     reason,
				StartedAt:  metav1.NewTime(a.startTime),
				FinishedAt: metav1.NewTime(a.terminatedAt),
			},
		})

		slog.Info("Child process terminated",
			"pid", a.pid,
			"exitCode", exitCode,
			"duration", a.terminatedAt.Sub(a.startTime))
	}()

	return nil
}

func (a *Agent) handleSigtermSignal() {
	go func() {
		proc, err := os.FindProcess(a.pid)
		if err != nil {
			return
		}

		_ = proc.Signal(syscall.SIGTERM)

		graceTimer := time.NewTimer(time.Duration(a.terminationGracePeriodSeconds) * time.Second)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		alive := func() bool { return proc.Signal(syscall.Signal(0)) == nil }

	Loop:
		for {
			select {
			case <-ticker.C:
				if !alive() {
					break Loop
				}
			case <-graceTimer.C:
				_ = proc.Signal(syscall.SIGKILL)
				for alive() {
					time.Sleep(100 * time.Millisecond)
				}
				break Loop
			}
		}
	}()
}

func (a *Agent) Run() (string, error) {
	slog.Debug("Running agent", "port", a.Port)

	// Do not start the child process automatically. Only run HTTP server and Wireproxy.
	slog.Info("Starting Wireproxy client")
	if a.EnableProxy {
		if err := a.startWireproxy(); err != nil {
			return "", fmt.Errorf("failed to start wireproxy: %w", err)
		}
	}

	slog.Info("Starting HTTP server", "address", a.server.Addr)
	if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return "", fmt.Errorf("failed to start HTTP server: %w", err)
	}

	return "Agent stopped", nil
}
