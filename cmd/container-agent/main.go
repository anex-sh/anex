package main

import (
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gitlab.devklarka.cz/ai/gpu-provider/internal/agent"
)

var (
	version = "0.1.0"
)

func setupLogger(level string) {
	var logLevel slog.Level
	switch strings.ToLower(level) {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})
	logger := slog.New(handler)
	slog.SetDefault(logger)
}

func main() {
	var logLevel string

	setupLogger("info")

	rootCmd := &cobra.Command{
		Use:   "container-agent",
		Short: "Container Agent for managing VastAI container",
		Long:  `Container Agent is a wrapper for container command in the VastAI execution environment.`,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			setupLogger(logLevel)
		},
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
		},
	}

	rootCmd.Version = version
	rootCmd.SetVersionTemplate("{{.Version}}\n")

	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "info", "Log level (debug, info, warn, error)")

	var port int
	var command string
	var authToken string
	var enablePromtail bool
	var enableProxy bool

	runCmd := &cobra.Command{
		Use:   "run [flags] -- <command> [args...]",
		Short: "Run the container agent",
		Long:  `Run the container agent to simulate pod runtime behavior. 
Pass the container command after -- to avoid flag parsing issues, e.g.:
  /container_agent run -p 8080 -- /bin/bash -c 'echo hello && sleep 1'`,
		Run: func(cmd *cobra.Command, args []string) {
			a := agent.NewAgent(port, command, "/etc/virtualpod")
			// Prefer argv provided after '--'; fallback to legacy -c if empty
			if len(args) > 0 {
				a.Argv = args
			}
			a.AuthToken = authToken
			a.EnablePromtail = enablePromtail
			a.EnableProxy = enableProxy
			result, err := a.Run()
			if err != nil {
				slog.Error("Failed to run agent", "error", err)
				os.Exit(1)
			}
			slog.Info("Agent completed", "result", result)
		},
	}

	runCmd.Flags().IntVarP(&port, "port", "p", 8080, "Port to listen on for HTTP server")
	runCmd.Flags().StringVarP(&command, "command", "c", "", "DEPRECATED: Command to run in the container. Prefer passing the command after --")
	runCmd.Flags().StringVarP(&authToken, "auth-token", "t", "", "Optional bearer token to authorize incoming HTTP requests")
	runCmd.Flags().BoolVar(&enablePromtail, "promtail", false, "Start promtail agent before wireproxy")
	runCmd.Flags().BoolVar(&enableProxy, "proxy", false, "Start wireproxy before running the main command")

	rootCmd.AddCommand(runCmd)

	if err := rootCmd.Execute(); err != nil {
		slog.Error("Failed to execute command", "error", err)
		os.Exit(1)
	}
}
