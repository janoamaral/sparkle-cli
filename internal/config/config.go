package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
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
	"config":        {Template: "{{.Input}}", Kind: "config"},
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
	cfg.SlashCommandsFile = os.ExpandEnv(cfg.SlashCommandsFile)
	cfg.SlashCommandsDir = os.ExpandEnv(cfg.SlashCommandsDir)
	if fileCommands, err := loadSlashCommandsFile(configPath, cfg.SlashCommandsFile); err != nil {
		return Config{}, "", err
	} else if len(fileCommands) > 0 {
		cfg.Commands = mergeCommandMaps(fileCommands, cfg.Commands)
	}
	if dirCommands, err := loadSlashCommandsDirectory(configPath, cfg.SlashCommandsDir); err != nil {
		return Config{}, "", err
	} else if len(dirCommands) > 0 {
		cfg.Commands = mergeCommandMaps(dirCommands, cfg.Commands)
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
	cfg.SlashCommandsFile = os.ExpandEnv(cfg.SlashCommandsFile)
	cfg.SlashCommandsDir = os.ExpandEnv(cfg.SlashCommandsDir)

	if len(cfg.Commands) == 0 {
		return
	}
	for name, command := range cfg.Commands {
		command.Prompt = os.ExpandEnv(command.Prompt)
		command.System = os.ExpandEnv(command.System)
		command.Template = os.ExpandEnv(command.Template)
		command.Model = os.ExpandEnv(command.Model)
		command.Kind = os.ExpandEnv(command.Kind)
		for index, param := range command.Params {
			command.Params[index] = os.ExpandEnv(param)
		}
		for index, param := range command.Optional {
			command.Optional[index] = os.ExpandEnv(param)
		}
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
	v.SetDefault("logs", false)
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
		normalizedCommands[strings.TrimPrefix(name, "/")] = normalizeSlashCommand(command)
	}
	return normalizedCommands
}

func applyDefaultCommands(cfg *Config) {
	for name, command := range defaultCommands {
		existing := cfg.Commands[name]
		existing.Template = firstNonEmpty(existing.Template, command.Template)
		existing.Prompt = firstNonEmpty(existing.Prompt, command.Prompt)
		existing.System = firstNonEmpty(existing.System, command.System)
		if len(existing.Params) == 0 {
			existing.Params = append([]string(nil), command.Params...)
		}
		if len(existing.Optional) == 0 {
			existing.Optional = append([]string(nil), command.Optional...)
		}
		existing.Model = firstNonEmpty(existing.Model, command.Model)
		existing.Kind = firstNonEmpty(existing.Kind, command.Kind)
		cfg.Commands[name] = normalizeSlashCommand(existing)
	}
}

func normalizeSlashCommand(command SlashCommand) SlashCommand {
	command.Template = strings.TrimSpace(command.Template)
	command.Prompt = strings.TrimSpace(command.Prompt)
	command.System = strings.TrimSpace(command.System)
	command.Model = strings.TrimSpace(command.Model)
	command.Kind = strings.TrimSpace(command.Kind)
	if command.Template == "" && command.Prompt != "" {
		command.Template = command.Prompt
	}
	if command.Prompt == "" && command.Template != "" {
		command.Prompt = command.Template
	}
	if len(command.Params) == 0 {
		command.Optional = normalizeParamList(command.Optional)
		return command
	}
	command.Params = normalizeParamList(command.Params)
	command.Optional = normalizeParamList(command.Optional)
	return command
}

func normalizeParamList(params []string) []string {
	if len(params) == 0 {
		return nil
	}
	normalizedParams := make([]string, 0, len(params))
	seen := make(map[string]struct{}, len(params))
	for _, param := range params {
		normalized := strings.TrimSpace(strings.TrimPrefix(param, "/"))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		normalizedParams = append(normalizedParams, normalized)
	}
	if len(normalizedParams) == 0 {
		return nil
	}
	return normalizedParams
}

func mergeCommandMaps(base map[string]SlashCommand, override map[string]SlashCommand) map[string]SlashCommand {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	merged := make(map[string]SlashCommand, len(base)+len(override))
	for name, command := range base {
		merged[name] = command
	}
	for name, command := range override {
		merged[name] = command
	}
	return merged
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
		if strings.TrimSpace(command.Template) == "" && strings.TrimSpace(command.Prompt) == "" {
			invalid = append(invalid, name)
		}
	}
	if len(invalid) > 0 {
		sort.Strings(invalid)
		return fmt.Errorf("config: commands with empty template: %s", strings.Join(invalid, ", "))
	}

	return nil
}

type slashCommandFileEntry struct {
	Command  string                 `yaml:"command"`
	Comando  string                 `yaml:"comando"`
	Desc     string                 `yaml:"desc"`
	Template string                 `yaml:"template"`
	Prompt   string                 `yaml:"prompt"`
	System   string                 `yaml:"system"`
	Params   slashCommandFileParams `yaml:"params"`
	Required []string               `yaml:"required_params"`
	Optional []string               `yaml:"optional_params"`
	Model    string                 `yaml:"model"`
	Kind     string                 `yaml:"kind"`
}

type slashCommandFileParams struct {
	Required []string
	Optional []string
}

func (params *slashCommandFileParams) UnmarshalYAML(node *yaml.Node) error {
	if isEmptyYAMLNode(node) {
		return nil
	}

	switch node.Kind {
	case yaml.SequenceNode:
		return params.unmarshalSequence(node)
	case yaml.MappingNode:
		return decodeSlashCommandParamMapping(node, params)
	default:
		return fmt.Errorf("unsupported params YAML shape")
	}
}

func isEmptyYAMLNode(node *yaml.Node) bool {
	if node == nil || node.Kind == 0 {
		return true
	}
	return node.Kind == yaml.ScalarNode && strings.TrimSpace(node.Value) == ""
}

func (params *slashCommandFileParams) unmarshalSequence(node *yaml.Node) error {
	if sequenceNodeHasOnlyScalars(node) {
		var required []string
		if err := node.Decode(&required); err != nil {
			return err
		}
		params.Required = append([]string(nil), required...)
		return nil
	}

	for _, item := range node.Content {
		if item.Kind != yaml.MappingNode {
			return fmt.Errorf("params sequence entries must be strings or mappings")
		}
		if err := decodeSlashCommandParamMapping(item, params); err != nil {
			return err
		}
	}
	return nil
}

func sequenceNodeHasOnlyScalars(node *yaml.Node) bool {
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode {
			return false
		}
	}
	return true
}

func decodeSlashCommandParamMapping(node *yaml.Node, params *slashCommandFileParams) error {
	requiredNode := mappingValue(node, "required")
	optionalNode := mappingValue(node, "optional")

	if requiredNode != nil {
		var required []string
		if err := requiredNode.Decode(&required); err != nil {
			return fmt.Errorf("params.required must be a list of strings: %w", err)
		}
		params.Required = append(params.Required, required...)
	}
	if optionalNode != nil {
		var optional []string
		if err := optionalNode.Decode(&optional); err != nil {
			return fmt.Errorf("params.optional must be a list of strings: %w", err)
		}
		params.Optional = append(params.Optional, optional...)
	}

	if requiredNode == nil && optionalNode == nil {
		return fmt.Errorf("params mapping must include required and/or optional")
	}

	return nil
}

func loadSlashCommandsFile(configPath string, configuredPath string) (map[string]SlashCommand, error) {
	configuredPath = strings.TrimSpace(configuredPath)
	if configuredPath == "" {
		return nil, nil
	}

	resolvedPath := configuredPath
	if !filepath.IsAbs(resolvedPath) {
		resolvedPath = filepath.Join(filepath.Dir(configPath), resolvedPath)
	}

	entryInfo, err := os.Stat(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("stat slash commands path %s: %w", resolvedPath, err)
	}

	if entryInfo.IsDir() {
		return loadSlashCommandsDirPath(resolvedPath)
	}

	commands, err := loadSlashCommandsYAMLFile(resolvedPath)
	if err != nil {
		return nil, err
	}
	return normalizeCommands(commands), nil
}

func loadSlashCommandsDirectory(configPath string, configuredDir string) (map[string]SlashCommand, error) {
	configuredDir = strings.TrimSpace(configuredDir)
	if configuredDir == "" {
		return nil, nil
	}

	resolvedPath := configuredDir
	if !filepath.IsAbs(resolvedPath) {
		resolvedPath = filepath.Join(filepath.Dir(configPath), resolvedPath)
	}

	entryInfo, err := os.Stat(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("stat slash commands directory %s: %w", resolvedPath, err)
	}
	if !entryInfo.IsDir() {
		return nil, fmt.Errorf("slash commands directory %s is not a directory", resolvedPath)
	}

	return loadSlashCommandsDirPath(resolvedPath)
}

func loadSlashCommandsDirPath(dirPath string) (map[string]SlashCommand, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("read slash commands directory %s: %w", dirPath, err)
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		files = append(files, filepath.Join(dirPath, entry.Name()))
	}
	sort.Strings(files)

	merged := map[string]SlashCommand{}
	for _, filePath := range files {
		commands, err := loadSlashCommandsYAMLFile(filePath)
		if err != nil {
			return nil, err
		}
		for name, command := range commands {
			merged[name] = command
		}
	}

	if len(merged) == 0 {
		return nil, nil
	}
	return normalizeCommands(merged), nil
}

func loadSlashCommandsYAMLFile(path string) (map[string]SlashCommand, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read slash commands file %s: %w", path, err)
	}

	commands, err := parseSlashCommandsYAML(contents)
	if err != nil {
		return nil, fmt.Errorf("decode slash commands file %s: %w", path, err)
	}
	return commands, nil
}

func parseSlashCommandsYAML(contents []byte) (map[string]SlashCommand, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(contents, &root); err != nil {
		return nil, err
	}
	if len(root.Content) == 0 {
		return nil, nil
	}
	return parseSlashCommandsNode(root.Content[0])
}

func parseSlashCommandsNode(node *yaml.Node) (map[string]SlashCommand, error) {
	if node == nil {
		return nil, nil
	}

	switch node.Kind {
	case yaml.SequenceNode:
		var entries []slashCommandFileEntry
		if err := node.Decode(&entries); err != nil {
			return nil, err
		}
		return slashCommandsFromEntries(entries)
	case yaml.MappingNode:
		if commandsNode := mappingValue(node, "commands"); commandsNode != nil {
			return parseSlashCommandsNode(commandsNode)
		}
		if mappingValue(node, "command") != nil || mappingValue(node, "comando") != nil {
			var entry slashCommandFileEntry
			if err := node.Decode(&entry); err != nil {
				return nil, err
			}
			return slashCommandsFromEntries([]slashCommandFileEntry{entry})
		}

		var commands map[string]SlashCommand
		if err := node.Decode(&commands); err != nil {
			return nil, err
		}
		return commands, nil
	default:
		return nil, fmt.Errorf("unsupported YAML shape for slash commands")
	}
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if strings.EqualFold(strings.TrimSpace(node.Content[index].Value), key) {
			return node.Content[index+1]
		}
	}
	return nil
}

func slashCommandsFromEntries(entries []slashCommandFileEntry) (map[string]SlashCommand, error) {
	commands := make(map[string]SlashCommand, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(firstNonEmpty(entry.Command, entry.Comando))
		if name == "" {
			return nil, fmt.Errorf("slash command entry missing command name")
		}
		commands[strings.TrimPrefix(name, "/")] = SlashCommand{
			Template: entry.Template,
			Prompt:   entry.Prompt,
			System:   entry.System,
			Params:   appendParamLists(entry.Params.Required, entry.Required),
			Optional: appendParamLists(entry.Params.Optional, entry.Optional),
			Model:    entry.Model,
			Kind:     entry.Kind,
			Desc:     entry.Desc,
		}
	}
	return commands, nil
}

func appendParamLists(base []string, extra []string) []string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make([]string, 0, len(base)+len(extra))
	out = append(out, base...)
	out = append(out, extra...)
	return out
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
