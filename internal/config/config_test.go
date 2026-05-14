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
	if _, ok := cfg.Commands["config"]; !ok {
		t.Fatal("expected default config command")
	}
	if got := cfg.Commands["search"].Kind; got != "search" {
		t.Fatalf("unexpected search kind: %s", got)
	}
	if got := cfg.Commands["config"].Kind; got != "config" {
		t.Fatalf("unexpected config kind: %s", got)
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
	if cfg.AgentFunctionModel != defaultAgentFunctionModel {
		t.Fatalf("unexpected agent function model: %s", cfg.AgentFunctionModel)
	}
	if cfg.AgentLuaDir != defaultAgentLuaDir {
		t.Fatalf("unexpected agent lua dir: %s", cfg.AgentLuaDir)
	}
	if cfg.AgentSkillsDir != defaultAgentSkillsDir {
		t.Fatalf("unexpected agent skills dir: %s", cfg.AgentSkillsDir)
	}
	if cfg.AgentMaxIterations != defaultAgentMaxIterations {
		t.Fatalf("unexpected agent max iterations: %d", cfg.AgentMaxIterations)
	}
	if cfg.AgentTimeoutSeconds != defaultAgentTimeoutSeconds {
		t.Fatalf("unexpected agent timeout seconds: %d", cfg.AgentTimeoutSeconds)
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
	if cfg.QdrantLookupLimit != defaultQdrantLookupLimit {
		t.Fatalf("unexpected qdrant lookup limit: %d", cfg.QdrantLookupLimit)
	}
	if cfg.QdrantMinRerankScore != defaultQdrantMinRerankScore {
		t.Fatalf("unexpected qdrant min rerank score: %f", cfg.QdrantMinRerankScore)
	}
	if cfg.QdrantLexicalWeight != defaultQdrantLexicalWeight {
		t.Fatalf("unexpected qdrant lexical weight: %f", cfg.QdrantLexicalWeight)
	}
	if cfg.QdrantSemanticWeight != defaultQdrantSemanticWeight {
		t.Fatalf("unexpected qdrant semantic weight: %f", cfg.QdrantSemanticWeight)
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
	if cfg.Logs {
		t.Fatal("expected logs to be disabled by default")
	}
	if cfg.Editor != defaultEditor {
		t.Fatalf("unexpected editor: %s", cfg.Editor)
	}
}

func TestLoadExplicitConfigOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	content := []byte("ollama_url: http://example.test:11434\nsearch_url: http://search.test/search\nsearch_query_model: gemma3:270m\nmodel: qwen2.5\ntimeout: 42\nlogs: true\neditor: visual studio code\ncommands:\n  fix:\n    template: 'Arregla: {{.Input}}'\n")
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
	if !cfg.Logs {
		t.Fatal("expected logs to be enabled when logs: true")
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

func TestLoadMergesSlashCommandsFromDedicatedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	slashPath := filepath.Join(dir, "slash-commands.yaml")
	configContent := []byte("slash_commands_file: ./slash-commands.yaml\ncommands:\n  fix:\n    template: 'Arregla inline: {{.Input}}'\n")
	slashContent := []byte("commands:\n  - comando: ticket\n    prompt: |\n      genera un ticket de Jira en el lenguaje {lang} a partir de la descripcion:\n      {input}\n    system: you are an expert software engineer that documents something\n    params: [lang]\n    model: Gemma4\n")
	if err := os.WriteFile(path, configContent, 0o644); err != nil {
		t.Fatalf(writeConfigErrFmt, err)
	}
	if err := os.WriteFile(slashPath, slashContent, 0o644); err != nil {
		t.Fatalf(writeConfigErrFmt, err)
	}

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf(loadErrFmt, err)
	}

	ticket, ok := cfg.Commands["ticket"]
	if !ok {
		t.Fatal("expected ticket command from dedicated slash commands file")
	}
	if ticket.Prompt == "" {
		t.Fatal("expected ticket prompt to be populated")
	}
	if ticket.System != "you are an expert software engineer that documents something" {
		t.Fatalf("unexpected ticket system prompt: %s", ticket.System)
	}
	if len(ticket.Params) != 1 || ticket.Params[0] != "lang" {
		t.Fatalf("unexpected ticket params: %#v", ticket.Params)
	}
	if ticket.Model != "Gemma4" {
		t.Fatalf("unexpected ticket model: %s", ticket.Model)
	}
	if cfg.Commands["fix"].Template != "Arregla inline: {{.Input}}" {
		t.Fatalf("unexpected inline fix command: %s", cfg.Commands["fix"].Template)
	}
	if _, ok := cfg.Commands["search"]; !ok {
		t.Fatal("expected default search command to remain available")
	}
}

func TestLoadSlashCommandsFromDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	slashDir := filepath.Join(dir, "slash-commands.d")

	configContent := []byte("slash_commands_dir: ./slash-commands.d\n")
	ticketContent := []byte("command: ticket\nprompt: |\n  genera un ticket de Jira en el lenguaje {lang} a partir de la descripcion:\n  {input}\nparams:\n  required: [lang]\n  optional: [role]\nmodel: Gemma4\n")
	incidentContent := []byte("command: incident\nprompt: |\n  resume este incidente con foco tecnico:\n  {input}\n")

	if err := os.WriteFile(path, configContent, 0o644); err != nil {
		t.Fatalf(writeConfigErrFmt, err)
	}
	if err := os.MkdirAll(slashDir, 0o755); err != nil {
		t.Fatalf("mkdir slash dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(slashDir, "10-ticket.yaml"), ticketContent, 0o644); err != nil {
		t.Fatalf(writeConfigErrFmt, err)
	}
	if err := os.WriteFile(filepath.Join(slashDir, "20-incident.yml"), incidentContent, 0o644); err != nil {
		t.Fatalf(writeConfigErrFmt, err)
	}

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf(loadErrFmt, err)
	}

	ticket, ok := cfg.Commands["ticket"]
	if !ok {
		t.Fatal("expected ticket command from slash commands directory")
	}
	if len(ticket.Params) != 1 || ticket.Params[0] != "lang" {
		t.Fatalf("unexpected required ticket params: %#v", ticket.Params)
	}
	if len(ticket.Optional) != 1 || ticket.Optional[0] != "role" {
		t.Fatalf("unexpected optional ticket params: %#v", ticket.Optional)
	}

	incident, ok := cfg.Commands["incident"]
	if !ok {
		t.Fatal("expected incident command from slash commands directory")
	}
	if len(incident.Params) != 0 {
		t.Fatalf("unexpected incident required params: %#v", incident.Params)
	}
}

func TestLoadMergesSlashCommandsFromFileAndDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	slashPath := filepath.Join(dir, "slash-commands.yaml")
	slashDir := filepath.Join(dir, "slash-commands.d")

	configContent := []byte("slash_commands_file: ./slash-commands.yaml\nslash_commands_dir: ./slash-commands.d\ncommands:\n  ticket:\n    prompt: 'inline ticket: {{.Input}}'\n")
	slashFileContent := []byte("commands:\n  - command: ticket\n    prompt: 'file ticket: {{.Input}}'\n  - command: from-file\n    prompt: 'from file: {{.Input}}'\n")
	slashDirContent := []byte("command: from-dir\nprompt: 'from dir: {{.Input}}'\n")

	if err := os.WriteFile(path, configContent, 0o644); err != nil {
		t.Fatalf(writeConfigErrFmt, err)
	}
	if err := os.WriteFile(slashPath, slashFileContent, 0o644); err != nil {
		t.Fatalf(writeConfigErrFmt, err)
	}
	if err := os.MkdirAll(slashDir, 0o755); err != nil {
		t.Fatalf("mkdir slash dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(slashDir, "10-from-dir.yaml"), slashDirContent, 0o644); err != nil {
		t.Fatalf(writeConfigErrFmt, err)
	}

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf(loadErrFmt, err)
	}

	if cfg.Commands["ticket"].Prompt != "inline ticket: {{.Input}}" {
		t.Fatalf("unexpected ticket prompt precedence: %s", cfg.Commands["ticket"].Prompt)
	}
	if cfg.Commands["from-file"].Prompt != "from file: {{.Input}}" {
		t.Fatalf("unexpected from-file command prompt: %s", cfg.Commands["from-file"].Prompt)
	}
	if cfg.Commands["from-dir"].Prompt != "from dir: {{.Input}}" {
		t.Fatalf("unexpected from-dir command prompt: %s", cfg.Commands["from-dir"].Prompt)
	}
}

func TestLoadSlashCommandsParsesLegacyAndExpandedParams(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	slashPath := filepath.Join(dir, "slash-commands.yaml")

	configContent := []byte("slash_commands_file: ./slash-commands.yaml\n")
	slashContent := []byte("commands:\n  - command: legacy\n    prompt: 'legacy {{.lang}} {{.Input}}'\n    params: [lang]\n  - command: structured\n    prompt: 'structured {{.lang}} {{.role}} {{.Input}}'\n    params:\n      - required: [lang]\n      - optional: [role]\n")

	if err := os.WriteFile(path, configContent, 0o644); err != nil {
		t.Fatalf(writeConfigErrFmt, err)
	}
	if err := os.WriteFile(slashPath, slashContent, 0o644); err != nil {
		t.Fatalf(writeConfigErrFmt, err)
	}

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf(loadErrFmt, err)
	}

	legacy := cfg.Commands["legacy"]
	if len(legacy.Params) != 1 || legacy.Params[0] != "lang" {
		t.Fatalf("unexpected legacy params: %#v", legacy.Params)
	}
	if len(legacy.Optional) != 0 {
		t.Fatalf("unexpected legacy optional params: %#v", legacy.Optional)
	}

	structured := cfg.Commands["structured"]
	if len(structured.Params) != 1 || structured.Params[0] != "lang" {
		t.Fatalf("unexpected structured required params: %#v", structured.Params)
	}
	if len(structured.Optional) != 1 || structured.Optional[0] != "role" {
		t.Fatalf("unexpected structured optional params: %#v", structured.Optional)
	}
}
