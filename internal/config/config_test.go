package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUsesDefaultsWhenFileMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg, path, err := Load("")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.OllamaURL != defaultOllamaURL {
		t.Fatalf("unexpected ollama url: %s", cfg.OllamaURL)
	}
	if cfg.Model != defaultModel {
		t.Fatalf("unexpected model: %s", cfg.Model)
	}
	if cfg.Timeout != defaultTimeout {
		t.Fatalf("unexpected timeout: %d", cfg.Timeout)
	}
	if cfg.Theme != defaultTheme {
		t.Fatalf("unexpected theme: %s", cfg.Theme)
	}
	if cfg.Editor != defaultEditor {
		t.Fatalf("unexpected editor: %s", cfg.Editor)
	}
	if path == "" {
		t.Fatal("expected config path")
	}
	if _, ok := cfg.Commands["explain"]; !ok {
		t.Fatal("expected default explain command")
	}
	if _, ok := cfg.Commands["generate-code"]; !ok {
		t.Fatal("expected default generate-code command")
	}
	if _, ok := cfg.Commands["translate"]; !ok {
		t.Fatal("expected default translate command")
	}
}

func TestLoadExplicitConfigOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("ollama_url: http://example.test:11434\nmodel: qwen2.5\ntimeout: 42\neditor: visual studio code\ncommands:\n  fix:\n    template: 'Arregla: {{.Input}}'\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.OllamaURL != "http://example.test:11434" {
		t.Fatalf("unexpected ollama url: %s", cfg.OllamaURL)
	}
	if cfg.Model != "qwen2.5" {
		t.Fatalf("unexpected model: %s", cfg.Model)
	}
	if cfg.Timeout != 42 {
		t.Fatalf("unexpected timeout: %d", cfg.Timeout)
	}
	if cfg.Theme != defaultTheme {
		t.Fatalf("unexpected theme: %s", cfg.Theme)
	}
	if cfg.Editor != "vscode" {
		t.Fatalf("unexpected editor: %s", cfg.Editor)
	}
	if cfg.Commands["fix"].Template != "Arregla: {{.Input}}" {
		t.Fatalf("unexpected fix template: %s", cfg.Commands["fix"].Template)
	}
	if _, ok := cfg.Commands["cheat"]; !ok {
		t.Fatal("expected default cheat command")
	}
}

func TestLoadRejectsUnsupportedEditor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("editor: nano\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, _, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want unsupported editor error")
	}
	if got := err.Error(); got != "config: editor must be one of neovim, vim, vscode, emacs" {
		t.Fatalf("Load() error = %q, want unsupported editor message", got)
	}
}
