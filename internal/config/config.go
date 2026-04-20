package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/viper"
)

const (
	defaultOllamaURL            = "http://localhost:11434"
	defaultSearchURL            = "https://search.nest.com.ar/search"
	defaultSearchEmbeddingModel = "nomic-embed-text"
	defaultSearchQueryModel     = "gemma3:270m"
	defaultModel                = "gemma4"
	defaultTimeout              = 30
	defaultSearchTimeout        = 60
	defaultLLMResolveTimeout    = 90
	defaultLLMTimeout           = 240
	defaultQdrantHost           = "qdrant.nest.com.ar"
	defaultQdrantPort           = 6334
	defaultQdrantCollection     = "semantic_cache"
	defaultQdrantScoreThreshold = 0.92
	defaultQdrantTTLHours       = 48
	defaultQdrantPoolSize       = 3
	defaultTheme                = "default"
	defaultEditor               = "neovim"
)

const defaultSystemPrompt = "You are a terminal expert. Produce concise, correct shell guidance and prefer returning a single command when the user is asking for one."

var defaultCommands = map[string]SlashCommand{
	"explain":       {Template: "Explica este comando de forma concisa: {{.Input}}"},
	"fix":           {Template: "Corrige los errores en este comando: {{.Input}}"},
	"cheat":         {Template: "Muestra ejemplos de uso para: {{.Input}}"},
	"generate-code": {Template: "Genera el comando de shell correspondiente a esta descripcion. Devuelve solo el comando, sin explicacion ni markdown: {{.Input}}"},
	"search":        {Template: "{{.Input}}", Kind: "search"},
	"translate": {Template: `Actúa como un traductor profesional con experiencia en localización lingüística. Tu tarea es traducir el texto delimitado por triples comillas invertidas al idioma {{.Language}}.
						Sigue estas restricciones técnicas:
						1. Precisión Semántica: Mantén el tono y el registro original (formal/informal).
						2. Preservación de Entidades: No traduzcas nombres propios, marcas o términos técnicos a menos que sea estándar en el idioma de destino.
						3. Formato de Salida: Devuelve ÚNICAMENTE el texto traducido. No incluyas introducciones ("Aquí tienes la traducción..."), etiquetas de Markdown, ni explicaciones post-procesamiento.
						Texto a traducir: {{.Text}}`, Model: "translategemma"},
}

var editorAliases = map[string]string{
	"neovim":             "neovim",
	"nvim":               "neovim",
	"vim":                "vim",
	"vscode":             "vscode",
	"code":               "vscode",
	"visual studio code": "vscode",
	"visual-studio-code": "vscode",
	"emacs":              "emacs",
}

func NormalizeEditor(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return defaultEditor, nil
	}

	resolved, ok := editorAliases[normalized]
	if !ok {
		return "", errors.New("config: editor must be one of neovim, vim, vscode, emacs")
	}

	return resolved, nil
}

func DefaultPath() (string, error) {
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve config dir: %w", err)
	}

	return filepath.Join(configRoot, "sparkle-cli", "config.yaml"), nil
}

func Load(explicitPath string) (Config, string, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetEnvPrefix("SPARKLE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)

	configPath := explicitPath
	if configPath == "" {
		var err error
		configPath, err = DefaultPath()
		if err != nil {
			return Config{}, "", err
		}
	}

	v.SetConfigFile(configPath)
	if err := v.ReadInConfig(); err != nil {
		var configNotFound viper.ConfigFileNotFoundError
		if !errors.As(err, &configNotFound) && !os.IsNotExist(err) {
			return Config{}, "", fmt.Errorf("read config %s: %w", configPath, err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, "", fmt.Errorf("decode config %s: %w", configPath, err)
	}
	expandEnvValues(&cfg)

	applyCommandDefaults(&cfg)
	applyTimeoutDefaults(&cfg,
		configValueIsSet(v, "timeout", "SPARKLE_TIMEOUT"),
		configValueIsSet(v, "search_timeout", "SPARKLE_SEARCH_TIMEOUT"),
		configValueIsSet(v, "llm_resolve_timeout", "SPARKLE_LLM_RESOLVE_TIMEOUT"),
		configValueIsSet(v, "llm_timeout", "SPARKLE_LLM_TIMEOUT"),
	)
	if err := validate(cfg); err != nil {
		return Config{}, "", err
	}

	return cfg, configPath, nil
}

func expandEnvValues(cfg *Config) {
	if cfg == nil {
		return
	}

	cfg.OllamaURL = os.ExpandEnv(cfg.OllamaURL)
	cfg.SearchURL = os.ExpandEnv(cfg.SearchURL)
	cfg.SearchEmbeddingModel = os.ExpandEnv(cfg.SearchEmbeddingModel)
	cfg.SearchQueryModel = os.ExpandEnv(cfg.SearchQueryModel)
	cfg.Model = os.ExpandEnv(cfg.Model)
	cfg.SystemPrompt = os.ExpandEnv(cfg.SystemPrompt)
	cfg.QdrantHost = os.ExpandEnv(cfg.QdrantHost)
	cfg.QdrantAPIKey = os.ExpandEnv(cfg.QdrantAPIKey)
	cfg.QdrantCollection = os.ExpandEnv(cfg.QdrantCollection)
	cfg.Theme = os.ExpandEnv(cfg.Theme)
	cfg.Editor = os.ExpandEnv(cfg.Editor)

	if len(cfg.Commands) == 0 {
		return
	}
	for name, command := range cfg.Commands {
		command.Template = os.ExpandEnv(command.Template)
		command.Model = os.ExpandEnv(command.Model)
		command.Kind = os.ExpandEnv(command.Kind)
		cfg.Commands[name] = command
	}
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("ollama_url", defaultOllamaURL)
	v.SetDefault("search_url", defaultSearchURL)
	v.SetDefault("search_embedding_model", defaultSearchEmbeddingModel)
	v.SetDefault("search_query_model", defaultSearchQueryModel)
	v.SetDefault("model", defaultModel)
	v.SetDefault("system_prompt", defaultSystemPrompt)
	v.SetDefault("timeout", defaultTimeout)
	v.SetDefault("qdrant_enabled", false)
	v.SetDefault("qdrant_host", defaultQdrantHost)
	v.SetDefault("qdrant_port", defaultQdrantPort)
	v.SetDefault("qdrant_use_tls", true)
	v.SetDefault("qdrant_collection", defaultQdrantCollection)
	v.SetDefault("qdrant_score_threshold", defaultQdrantScoreThreshold)
	v.SetDefault("qdrant_ttl_hours", defaultQdrantTTLHours)
	v.SetDefault("qdrant_pool_size", defaultQdrantPoolSize)
	v.SetDefault("theme", defaultTheme)
	v.SetDefault("editor", defaultEditor)

	commands := make(map[string]map[string]string, len(defaultCommands))
	for name, command := range defaultCommands {
		entry := map[string]string{"template": command.Template}
		if strings.TrimSpace(command.Model) != "" {
			entry["model"] = command.Model
		}
		if strings.TrimSpace(command.Kind) != "" {
			entry["kind"] = command.Kind
		}
		commands[name] = entry
	}
	v.SetDefault("commands", commands)
}

func applyCommandDefaults(cfg *Config) {
	if cfg.Commands == nil {
		cfg.Commands = map[string]SlashCommand{}
	}

	cfg.Commands = normalizeCommands(cfg.Commands)
	applyDefaultCommands(cfg)
	if strings.TrimSpace(cfg.Theme) == "" {
		cfg.Theme = defaultTheme
	}
	if strings.TrimSpace(cfg.OllamaURL) == "" {
		cfg.OllamaURL = defaultOllamaURL
	}
	if strings.TrimSpace(cfg.SearchURL) == "" {
		cfg.SearchURL = defaultSearchURL
	}
	if strings.TrimSpace(cfg.SearchEmbeddingModel) == "" {
		cfg.SearchEmbeddingModel = defaultSearchEmbeddingModel
	}
	if strings.TrimSpace(cfg.SearchQueryModel) == "" {
		cfg.SearchQueryModel = defaultSearchQueryModel
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = defaultModel
	}
	if strings.TrimSpace(cfg.QdrantHost) == "" {
		cfg.QdrantHost = defaultQdrantHost
	}
	if cfg.QdrantPort <= 0 {
		cfg.QdrantPort = defaultQdrantPort
	}
	if strings.TrimSpace(cfg.QdrantCollection) == "" {
		cfg.QdrantCollection = defaultQdrantCollection
	}
	if cfg.QdrantScoreThreshold <= 0 {
		cfg.QdrantScoreThreshold = defaultQdrantScoreThreshold
	}
	if cfg.QdrantTTLHours <= 0 {
		cfg.QdrantTTLHours = defaultQdrantTTLHours
	}
	if cfg.QdrantPoolSize <= 0 {
		cfg.QdrantPoolSize = defaultQdrantPoolSize
	}
	if strings.TrimSpace(cfg.SystemPrompt) == "" {
		cfg.SystemPrompt = defaultSystemPrompt
	}
	if normalizedEditor, err := NormalizeEditor(cfg.Editor); err == nil {
		cfg.Editor = normalizedEditor
	}
}

func applyTimeoutDefaults(cfg *Config, timeoutSet bool, searchTimeoutSet bool, llmResolveTimeoutSet bool, llmTimeoutSet bool) {
	legacyTimeout := cfg.Timeout
	if legacyTimeout <= 0 {
		legacyTimeout = defaultTimeout
	}
	if cfg.SearchTimeout <= 0 {
		switch {
		case searchTimeoutSet:
		case timeoutSet:
			cfg.SearchTimeout = legacyTimeout
		default:
			cfg.SearchTimeout = defaultSearchTimeout
		}
	}
	if cfg.LLMResolveTimeout <= 0 {
		switch {
		case llmResolveTimeoutSet:
		case llmTimeoutSet:
			cfg.LLMResolveTimeout = cfg.LLMTimeout
		case timeoutSet:
			cfg.LLMResolveTimeout = legacyTimeout
		default:
			cfg.LLMResolveTimeout = defaultLLMResolveTimeout
		}
	}
	if cfg.LLMTimeout <= 0 {
		switch {
		case llmTimeoutSet:
		case timeoutSet:
			cfg.LLMTimeout = legacyTimeout
		default:
			cfg.LLMTimeout = defaultLLMTimeout
		}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
}

func normalizeCommands(commands map[string]SlashCommand) map[string]SlashCommand {
	normalizedCommands := make(map[string]SlashCommand, len(commands))
	for name, command := range commands {
		normalizedCommands[strings.TrimPrefix(name, "/")] = command
	}
	return normalizedCommands
}

func applyDefaultCommands(cfg *Config) {
	for name, command := range defaultCommands {
		existing := cfg.Commands[name]
		existing.Template = firstNonEmpty(existing.Template, command.Template)
		existing.Model = firstNonEmpty(existing.Model, command.Model)
		existing.Kind = firstNonEmpty(existing.Kind, command.Kind)
		cfg.Commands[name] = existing
	}
}

func firstNonEmpty(current string, fallback string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return fallback
}

func configValueIsSet(v *viper.Viper, key string, envKey string) bool {
	if v.InConfig(key) {
		return true
	}
	_, ok := os.LookupEnv(envKey)
	return ok
}

func validate(cfg Config) error {
	if err := validateRequiredConfig(cfg); err != nil {
		return err
	}
	if err := validateQdrantConfig(cfg); err != nil {
		return err
	}
	if _, err := NormalizeEditor(cfg.Editor); err != nil {
		return err
	}

	invalid := make([]string, 0)
	for name, command := range cfg.Commands {
		if strings.TrimSpace(command.Template) == "" {
			invalid = append(invalid, name)
		}
	}
	if len(invalid) > 0 {
		sort.Strings(invalid)
		return fmt.Errorf("config: commands with empty template: %s", strings.Join(invalid, ", "))
	}

	return nil
}

func validateRequiredConfig(cfg Config) error {
	requiredFields := []struct {
		value string
		err   string
	}{
		{cfg.OllamaURL, "config: ollama_url is required"},
		{cfg.SearchURL, "config: search_url is required"},
		{cfg.SearchEmbeddingModel, "config: search_embedding_model is required"},
		{cfg.SearchQueryModel, "config: search_query_model is required"},
		{cfg.Model, "config: model is required"},
		{cfg.SystemPrompt, "config: system_prompt is required"},
	}
	for _, field := range requiredFields {
		if strings.TrimSpace(field.value) == "" {
			return errors.New(field.err)
		}
	}

	positiveTimeouts := []struct {
		value int
		err   string
	}{
		{cfg.SearchTimeout, "config: search_timeout must be greater than zero"},
		{cfg.LLMResolveTimeout, "config: llm_resolve_timeout must be greater than zero"},
		{cfg.LLMTimeout, "config: llm_timeout must be greater than zero"},
	}
	if cfg.Timeout < 0 {
		return errors.New("config: timeout must not be negative")
	}
	for _, field := range positiveTimeouts {
		if field.value <= 0 {
			return errors.New(field.err)
		}
	}

	return nil
}

func validateQdrantConfig(cfg Config) error {
	if !cfg.QdrantEnabled {
		return nil
	}
	if strings.TrimSpace(cfg.QdrantHost) == "" {
		return errors.New("config: qdrant_host is required when qdrant_enabled is true")
	}
	if cfg.QdrantPort <= 0 {
		return errors.New("config: qdrant_port must be greater than zero when qdrant_enabled is true")
	}
	if strings.TrimSpace(cfg.QdrantCollection) == "" {
		return errors.New("config: qdrant_collection is required when qdrant_enabled is true")
	}
	if cfg.QdrantScoreThreshold <= 0 || cfg.QdrantScoreThreshold > 1 {
		return errors.New("config: qdrant_score_threshold must be between 0 and 1")
	}
	if cfg.QdrantTTLHours <= 0 {
		return errors.New("config: qdrant_ttl_hours must be greater than zero when qdrant_enabled is true")
	}
	if cfg.QdrantPoolSize <= 0 {
		return errors.New("config: qdrant_pool_size must be greater than zero when qdrant_enabled is true")
	}
	return nil
}
