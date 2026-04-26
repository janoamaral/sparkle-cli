package tui

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxSessionLogCreateAttempts = 5

type sessionLogger struct {
	mu   sync.Mutex
	file *os.File
	path string
}

func newSessionLogger(configPath string) (*sessionLogger, error) {
	configPath = strings.TrimSpace(configPath)
	configDir := "."
	if configPath != "" {
		configDir = filepath.Dir(configPath)
	}

	for attempt := 0; attempt < maxSessionLogCreateAttempts; attempt++ {
		sessionID, err := randomSessionID()
		if err != nil {
			return nil, err
		}

		logPath := filepath.Join(configDir, fmt.Sprintf("session-%d.log", sessionID))
		file, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return nil, fmt.Errorf("create session log %s: %w", logPath, err)
		}

		logger := &sessionLogger{file: file, path: logPath}
		_ = logger.logEntry("session_start", fmt.Sprintf("config_path: %s\nlog_path: %s", configPath, logPath))
		return logger, nil
	}

	return nil, fmt.Errorf("unable to create unique session log in %s", configDir)
}

func randomSessionID() (int64, error) {
	max := big.NewInt(900000000)
	value, err := rand.Int(rand.Reader, max)
	if err != nil {
		return 0, fmt.Errorf("generate session log id: %w", err)
	}
	return value.Int64() + 100000000, nil
}

func (l *sessionLogger) logEntry(label string, content string) error {
	if l == nil || l.file == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	timestamp := time.Now().Format(time.RFC3339Nano)
	normalized := strings.TrimSpace(content)
	if normalized == "" {
		normalized = "<empty>"
	}

	if _, err := fmt.Fprintf(l.file, "[%s] %s\n%s\n\n", timestamp, label, normalized); err != nil {
		return fmt.Errorf("write session log %s: %w", l.path, err)
	}
	return l.file.Sync()
}

func (l *sessionLogger) close() error {
	if l == nil || l.file == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	err := l.file.Close()
	l.file = nil
	if err != nil {
		return fmt.Errorf("close session log %s: %w", l.path, err)
	}
	return nil
}
