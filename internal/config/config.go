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
)

const defaultSystemPrompt = "You are a terminal expert. Produce concise, correct shell guidance and prefer returning a single command when the user is asking for one."

var defaultCommands = map[string]SlashCommand{
	"explain":       {Template: "Explica este comando de forma concisa: {{.Input}}"},
	"fix":           {Template: "Corrige los errores en este comando: {{.Input}}"},
	"cheat":         {Template: "Muestra ejemplos de uso para: {{.Input}}"},
	"generate-code": {Template: "Genera el comando de shell correspondiente a esta descripcion. Devuelve solo el comando, sin explicacion ni markdown: {{.Input}}"},
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

	for name, command := range defaultCommands {
		if existing, ok := cfg.Commands[name]; ok && strings.TrimSpace(existing.Template) != "" {
			continue
		}
		cfg.Commands[name] = command
	}
	for name, command := range cfg.Commands {
		cfg.Commands[strings.TrimPrefix(name, "/")] = command
		if strings.HasPrefix(name, "/") {
			delete(cfg.Commands, name)
		}
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
