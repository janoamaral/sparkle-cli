package config

type SlashCommand struct {
	Template string `mapstructure:"template"`
}

type Config struct {
	OllamaURL    string                  `mapstructure:"ollama_url"`
	Model        string                  `mapstructure:"model"`
	SystemPrompt string                  `mapstructure:"system_prompt"`
	Timeout      int                     `mapstructure:"timeout"`
	Commands     map[string]SlashCommand `mapstructure:"commands"`
}
