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
	defaultOllamaURL = "http://localhost:11434"
	defaultModel     = "gemma4"
	defaultTimeout   = 30
	defaultTheme     = "default"
	defaultEditor    = "neovim"
)

const defaultSystemPrompt = "You are a terminal expert. Produce concise, correct shell guidance and prefer returning a single command when the user is asking for one."

var defaultCommands = map[string]SlashCommand{
	"explain":       {Template: "Explica este comando de forma concisa: {{.Input}}"},
	"fix":           {Template: "Corrige los errores en este comando: {{.Input}}"},
	"cheat":         {Template: "Muestra ejemplos de uso para: {{.Input}}"},
	"generate-code": {Template: "Genera el comando de shell correspondiente a esta descripcion. Devuelve solo el comando, sin explicacion ni markdown: {{.Input}}"},
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

	applyCommandDefaults(&cfg)
	if err := validate(cfg); err != nil {
		return Config{}, "", err
	}

	return cfg, configPath, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("ollama_url", defaultOllamaURL)
	v.SetDefault("model", defaultModel)
	v.SetDefault("system_prompt", defaultSystemPrompt)
	v.SetDefault("timeout", defaultTimeout)
	v.SetDefault("theme", defaultTheme)
	v.SetDefault("editor", defaultEditor)

	commands := make(map[string]map[string]string, len(defaultCommands))
	for name, command := range defaultCommands {
		commands[name] = map[string]string{"template": command.Template}
	}
	v.SetDefault("commands", commands)
}

func applyCommandDefaults(cfg *Config) {
	if cfg.Commands == nil {
		cfg.Commands = map[string]SlashCommand{}
	}

	normalizedCommands := make(map[string]SlashCommand, len(cfg.Commands))
	for name, command := range cfg.Commands {
		normalizedCommands[strings.TrimPrefix(name, "/")] = command
	}
	cfg.Commands = normalizedCommands

	for name, command := range defaultCommands {
		existing := cfg.Commands[name]
		if strings.TrimSpace(existing.Template) == "" {
			existing.Template = command.Template
		}
		if strings.TrimSpace(existing.Model) == "" {
			existing.Model = command.Model
		}
		cfg.Commands[name] = existing
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	if strings.TrimSpace(cfg.Theme) == "" {
		cfg.Theme = defaultTheme
	}
	if strings.TrimSpace(cfg.OllamaURL) == "" {
		cfg.OllamaURL = defaultOllamaURL
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = defaultModel
	}
	if strings.TrimSpace(cfg.SystemPrompt) == "" {
		cfg.SystemPrompt = defaultSystemPrompt
	}
	if normalizedEditor, err := NormalizeEditor(cfg.Editor); err == nil {
		cfg.Editor = normalizedEditor
	}
}

func validate(cfg Config) error {
	if strings.TrimSpace(cfg.OllamaURL) == "" {
		return errors.New("config: ollama_url is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return errors.New("config: model is required")
	}
	if strings.TrimSpace(cfg.SystemPrompt) == "" {
		return errors.New("config: system_prompt is required")
	}
	if cfg.Timeout <= 0 {
		return errors.New("config: timeout must be greater than zero")
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
