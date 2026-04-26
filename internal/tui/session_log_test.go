package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewSessionLoggerCreatesFileInConfigDirectory(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	logger, err := newSessionLogger(configPath)
	if err != nil {
		t.Fatalf("newSessionLogger() error = %v", err)
	}
	defer func() { _ = logger.close() }()

	if filepath.Dir(logger.path) != tempDir {
		t.Fatalf("logger dir = %q, want %q", filepath.Dir(logger.path), tempDir)
	}
	if !strings.HasPrefix(filepath.Base(logger.path), "session-") || !strings.HasSuffix(filepath.Base(logger.path), ".log") {
		t.Fatalf("logger path = %q, want session-[random].log", logger.path)
	}
}

func TestSessionLoggerWritesTimestampedEntries(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	logger, err := newSessionLogger(configPath)
	if err != nil {
		t.Fatalf("newSessionLogger() error = %v", err)
	}

	if err := logger.logEntry("user_input", "hola mundo"); err != nil {
		t.Fatalf("logEntry() error = %v", err)
	}
	if err := logger.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}

	content, err := os.ReadFile(logger.path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "session_start") {
		t.Fatalf("log file missing session_start entry: %q", text)
	}
	if !strings.Contains(text, "user_input") {
		t.Fatalf("log file missing user_input entry: %q", text)
	}
	if !strings.Contains(text, "hola mundo") {
		t.Fatalf("log file missing entry content: %q", text)
	}
	if !strings.Contains(text, "[") {
		t.Fatalf("log file missing timestamp markers: %q", text)
	}
}
