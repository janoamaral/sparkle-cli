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
	if path == "" {
		t.Fatal("expected config path")
	}
	if _, ok := cfg.Commands["explain"]; !ok {
		t.Fatal("expected default explain command")
	}
	if _, ok := cfg.Commands["generate-code"]; !ok {
		t.Fatal("expected default generate-code command")
	}
}

func TestLoadExplicitConfigOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("ollama_url: http://example.test:11434\nmodel: qwen2.5\ntimeout: 42\ncommands:\n  fix:\n    template: 'Arregla: {{.Input}}'\n")
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
	if cfg.Commands["fix"].Template != "Arregla: {{.Input}}" {
		t.Fatalf("unexpected fix template: %s", cfg.Commands["fix"].Template)
	}
	if _, ok := cfg.Commands["cheat"]; !ok {
		t.Fatal("expected default cheat command")
	}
}
