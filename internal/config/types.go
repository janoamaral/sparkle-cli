package config

type SlashCommand struct {
	Template string `mapstructure:"template"`
	Model    string `mapstructure:"model"`
	Kind     string `mapstructure:"kind"`
}

type Config struct {
	OllamaURL         string                  `mapstructure:"ollama_url"`
	SearchURL         string                  `mapstructure:"search_url"`
	Model             string                  `mapstructure:"model"`
	SystemPrompt      string                  `mapstructure:"system_prompt"`
	Timeout           int                     `mapstructure:"timeout"`
	SearchTimeout     int                     `mapstructure:"search_timeout"`
	LLMResolveTimeout int                     `mapstructure:"llm_resolve_timeout"`
	LLMTimeout        int                     `mapstructure:"llm_timeout"`
	Theme             string                  `mapstructure:"theme"`
	Editor            string                  `mapstructure:"editor"`
	Commands          map[string]SlashCommand `mapstructure:"commands"`
}
