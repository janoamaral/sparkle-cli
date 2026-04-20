package config

import (
	"os"
	"path/filepath"
	"testing"
)

const (
	configFileName                 = "config.yaml"
	loadErrFmt                     = "Load() error = %v"
	writeConfigErrFmt              = "write config: %v"
	unexpectedTimeoutFmt           = "unexpected timeout: %d"
	unexpectedSearchTimeoutFmt     = "unexpected search timeout: %d"
	unexpectedLLMResolveTimeoutFmt = "unexpected llm resolve timeout: %d"
	unexpectedLLMTimeoutFmt        = "unexpected llm timeout: %d"
)

func TestLoadUsesDefaultsWhenFileMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg, path, err := Load("")
	if err != nil {
		t.Fatalf(loadErrFmt, err)
	}
	assertDefaultConfig(t, cfg)
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
	if got := cfg.Commands["search"].Kind; got != "search" {
		t.Fatalf("unexpected search kind: %s", got)
	}
}

func assertDefaultConfig(t *testing.T, cfg Config) {
	t.Helper()
	if cfg.OllamaURL != defaultOllamaURL {
		t.Fatalf("unexpected ollama url: %s", cfg.OllamaURL)
	}
	if cfg.SearchURL != defaultSearchURL {
		t.Fatalf("unexpected search url: %s", cfg.SearchURL)
	}
	if cfg.SearchEmbeddingModel != defaultSearchEmbeddingModel {
		t.Fatalf("unexpected search embedding model: %s", cfg.SearchEmbeddingModel)
	}
	if cfg.SearchQueryModel != defaultSearchQueryModel {
		t.Fatalf("unexpected search query model: %s", cfg.SearchQueryModel)
	}
	if cfg.Model != defaultModel {
		t.Fatalf("unexpected model: %s", cfg.Model)
	}
	if cfg.QdrantEnabled {
		t.Fatal("expected qdrant to be disabled by default")
	}
	if cfg.QdrantHost != defaultQdrantHost {
		t.Fatalf("unexpected qdrant host: %s", cfg.QdrantHost)
	}
	if cfg.QdrantPort != defaultQdrantPort {
		t.Fatalf("unexpected qdrant port: %d", cfg.QdrantPort)
	}
	if cfg.QdrantCollection != defaultQdrantCollection {
		t.Fatalf("unexpected qdrant collection: %s", cfg.QdrantCollection)
	}
	if cfg.Timeout != defaultTimeout {
		t.Fatalf(unexpectedTimeoutFmt, cfg.Timeout)
	}
	if cfg.SearchTimeout != defaultSearchTimeout {
		t.Fatalf(unexpectedSearchTimeoutFmt, cfg.SearchTimeout)
	}
	if cfg.LLMResolveTimeout != defaultLLMResolveTimeout {
		t.Fatalf(unexpectedLLMResolveTimeoutFmt, cfg.LLMResolveTimeout)
	}
	if cfg.LLMTimeout != defaultLLMTimeout {
		t.Fatalf(unexpectedLLMTimeoutFmt, cfg.LLMTimeout)
	}
	if cfg.Theme != defaultTheme {
		t.Fatalf("unexpected theme: %s", cfg.Theme)
	}
	if cfg.Editor != defaultEditor {
		t.Fatalf("unexpected editor: %s", cfg.Editor)
	}
}

func TestLoadExplicitConfigOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	content := []byte("ollama_url: http://example.test:11434\nsearch_url: http://search.test/search\nsearch_query_model: gemma3:270m\nmodel: qwen2.5\ntimeout: 42\neditor: visual studio code\ncommands:\n  fix:\n    template: 'Arregla: {{.Input}}'\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf(writeConfigErrFmt, err)
	}

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf(loadErrFmt, err)
	}

	if cfg.OllamaURL != "http://example.test:11434" {
		t.Fatalf("unexpected ollama url: %s", cfg.OllamaURL)
	}
	if cfg.SearchURL != "http://search.test/search" {
		t.Fatalf("unexpected search url: %s", cfg.SearchURL)
	}
	if cfg.SearchEmbeddingModel != defaultSearchEmbeddingModel {
		t.Fatalf("unexpected search embedding model: %s", cfg.SearchEmbeddingModel)
	}
	if cfg.SearchQueryModel != "gemma3:270m" {
		t.Fatalf("unexpected search query model: %s", cfg.SearchQueryModel)
	}
	if cfg.Model != "qwen2.5" {
		t.Fatalf("unexpected model: %s", cfg.Model)
	}
	if cfg.Timeout != 42 {
		t.Fatalf(unexpectedTimeoutFmt, cfg.Timeout)
	}
	if cfg.SearchTimeout != 42 {
		t.Fatalf(unexpectedSearchTimeoutFmt, cfg.SearchTimeout)
	}
	if cfg.LLMResolveTimeout != 42 {
		t.Fatalf(unexpectedLLMResolveTimeoutFmt, cfg.LLMResolveTimeout)
	}
	if cfg.LLMTimeout != 42 {
		t.Fatalf(unexpectedLLMTimeoutFmt, cfg.LLMTimeout)
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

func TestLoadRejectsInvalidQdrantThresholdWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	content := []byte("qdrant_enabled: true\nqdrant_score_threshold: 1.5\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf(writeConfigErrFmt, err)
	}

	_, _, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want invalid qdrant threshold error")
	}
	if got := err.Error(); got != "config: qdrant_score_threshold must be between 0 and 1" {
		t.Fatalf("Load() error = %q, want invalid qdrant threshold message", got)
	}
}

func TestLoadSpecificSearchAndLLMTimeoutsOverrideLegacyTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	content := []byte("timeout: 42\nsearch_timeout: 70\nllm_resolve_timeout: 120\nllm_timeout: 240\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf(writeConfigErrFmt, err)
	}

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf(loadErrFmt, err)
	}

	if cfg.Timeout != 42 {
		t.Fatalf(unexpectedTimeoutFmt, cfg.Timeout)
	}
	if cfg.SearchTimeout != 70 {
		t.Fatalf(unexpectedSearchTimeoutFmt, cfg.SearchTimeout)
	}
	if cfg.LLMResolveTimeout != 120 {
		t.Fatalf(unexpectedLLMResolveTimeoutFmt, cfg.LLMResolveTimeout)
	}
	if cfg.LLMTimeout != 240 {
		t.Fatalf(unexpectedLLMTimeoutFmt, cfg.LLMTimeout)
	}
}

func TestLoadExpandsEnvironmentVariablesInConfigValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	t.Setenv("QDRANT_API_KEY", "secret-token")
	t.Setenv("SEARCH_URL_OVERRIDE", "https://search.example.test/search")
	content := []byte("search_url: ${SEARCH_URL_OVERRIDE}\nqdrant_enabled: true\nqdrant_api_key: ${QDRANT_API_KEY}\ncommands:\n  fix:\n    template: 'Token: ${QDRANT_API_KEY}'\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf(writeConfigErrFmt, err)
	}

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf(loadErrFmt, err)
	}
	if cfg.SearchURL != "https://search.example.test/search" {
		t.Fatalf("unexpected expanded search url: %s", cfg.SearchURL)
	}
	if cfg.QdrantAPIKey != "secret-token" {
		t.Fatalf("unexpected expanded qdrant api key: %s", cfg.QdrantAPIKey)
	}
	if cfg.Commands["fix"].Template != "Token: secret-token" {
		t.Fatalf("unexpected expanded fix template: %s", cfg.Commands["fix"].Template)
	}
}

func TestLoadRejectsUnsupportedEditor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	content := []byte("editor: nano\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf(writeConfigErrFmt, err)
	}

	_, _, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want unsupported editor error")
	}
	if got := err.Error(); got != "config: editor must be one of neovim, vim, vscode, emacs" {
		t.Fatalf("Load() error = %q, want unsupported editor message", got)
	}
}
