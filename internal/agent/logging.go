package agent

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	// DefaultLogDir is the default directory for log files
	DefaultLogDir = "/var/log/agent"
	// DefaultMaxSize is the default maximum size of a log file in bytes (10MB)
	DefaultMaxSize int64 = 10 * 1024 * 1024
	// DefaultMaxFiles is the default maximum number of log files to keep
	DefaultMaxFiles = 5
	// User and group ID for file ownership
	UserID  = 0
	GroupID = 0

	// DefaultLogDir = "/home/skarupa/glami/vastai-container-agent"
	// UserID  = 1000
	// GroupID = 1000
)

// setFileOwnership changes the ownership of a file or directory to the specified user and group
func setFileOwnership(path string) error {
	return syscall.Chown(path, UserID, GroupID)
}

// Logger is a simple logger that writes to a file with rotation
type Logger struct {
	dir      string
	prefix   string
	maxSize  int64
	maxFiles int
	mu       sync.Mutex
	file     *os.File
	size     int64
}

// NewLogger creates a new logger with the given options
func NewLogger(dir, prefix string, maxSize int64, maxFiles int) (*Logger, error) {
	if dir == "" {
		dir = DefaultLogDir
	}
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	if maxFiles <= 0 {
		maxFiles = DefaultMaxFiles
	}

	// Create log directory if it doesn't exist
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// TODO: Check - requires too much permissions
	// Set ownership of the log directory to the specified user and group
	//if err := setFileOwnership(dir); err != nil {
	//	return nil, fmt.Errorf("failed to set ownership of log directory: %w", err)
	//}

	logger := &Logger{
		dir:      dir,
		prefix:   prefix,
		maxSize:  maxSize,
		maxFiles: maxFiles,
	}

	if err := logger.openFile(); err != nil {
		return nil, err
	}

	return logger, nil
}

// openFile opens a new log file
func (l *Logger) openFile() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Close existing file if open
	if l.file != nil {
		l.file.Close()
		l.file = nil
		l.size = 0
	}

	// Create a new log file with timestamp
	// timestamp := time.Now().Format("20060102-150405.000")
	filename := fmt.Sprintf("%s.log", l.prefix)
	path := filepath.Join(l.dir, filename)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	// TODO: ownership - permissions
	// Set ownership of the log file to the specified user and group
	//if err := setFileOwnership(path); err != nil {
	//	file.Close()
	//	return fmt.Errorf("failed to set ownership of log file: %w", err)
	//}

	l.file = file
	l.size = 0

	// Clean up old log files
	l.cleanupOldFiles()

	return nil
}

// cleanupOldFiles removes old log files if there are more than maxFiles
func (l *Logger) cleanupOldFiles() {
	files, err := filepath.Glob(filepath.Join(l.dir, l.prefix+"-*.log"))
	if err != nil {
		return
	}

	if len(files) <= l.maxFiles {
		return
	}

	// Sort files by modification time (oldest first)
	type fileInfo struct {
		path    string
		modTime time.Time
	}
	fileInfos := make([]fileInfo, 0, len(files))
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		fileInfos = append(fileInfos, fileInfo{path: file, modTime: info.ModTime()})
	}

	// Sort by modification time (oldest first)
	for i := 0; i < len(fileInfos); i++ {
		for j := i + 1; j < len(fileInfos); j++ {
			if fileInfos[i].modTime.After(fileInfos[j].modTime) {
				fileInfos[i], fileInfos[j] = fileInfos[j], fileInfos[i]
			}
		}
	}

	// Remove oldest files
	for i := 0; i < len(fileInfos)-l.maxFiles; i++ {
		os.Remove(fileInfos[i].path)
	}
}

// Write writes data to the log file with rotation
func (l *Logger) Write(p []byte) (n int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Check if we need to rotate
	if l.size+int64(len(p)) > l.maxSize {
		if err := l.openFile(); err != nil {
			return 0, err
		}
	}

	// Write to file
	n, err = l.file.Write(p)
	if err == nil {
		l.size += int64(n)
	}
	return n, err
}

// Close closes the logger
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		err := l.file.Close()
		l.file = nil
		l.size = 0
		return err
	}
	return nil
}

// StreamWithTimestamp reads from r and writes to the logger with RFC 3339 nano timestamps
func StreamWithTimestamp(r io.Reader, w io.Writer) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		timestamp := time.Now().Format(time.RFC3339Nano)
		_, _ = w.Write([]byte(timestamp + " " + line + "\n"))
	}
}

// SetupLogging sets up logging for stdout and stderr
// If stdoutPrefix and stderrPrefix are the same, both streams will be logged to the same file
func SetupLogging(stdoutPrefix, stderrPrefix string) (io.WriteCloser, io.WriteCloser, error) {
	// Create logger for stdout
	stdoutLogger, err := NewLogger(DefaultLogDir, stdoutPrefix, DefaultMaxSize, DefaultMaxFiles)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create stdout logger: %w", err)
	}

	// If stderr prefix is the same as stdout prefix, use the same logger
	var stderrLogger *Logger
	if stdoutPrefix == stderrPrefix {
		stderrLogger = stdoutLogger
	} else {
		// Create separate logger for stderr
		stderrLogger, err = NewLogger(DefaultLogDir, stderrPrefix, DefaultMaxSize, DefaultMaxFiles)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create stderr logger: %w", err)
		}
	}

	// Create pipes for stdout and stderr
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()

	// Start goroutines to read from pipes and write to loggers with timestamps
	go func() {
		defer stdoutLogger.Close()
		StreamWithTimestamp(stdoutR, stdoutLogger)
	}()

	go func() {
		defer stderrLogger.Close()
		StreamWithTimestamp(stderrR, stderrLogger)
	}()

	return stdoutW, stderrW, nil
}
