package config

type SlashCommand struct {
	Template string `mapstructure:"template"`
	Model    string `mapstructure:"model"`
	Kind     string `mapstructure:"kind"`
}

type Config struct {
	OllamaURL            string                  `mapstructure:"ollama_url"`
	SearchURL            string                  `mapstructure:"search_url"`
	SearchEmbeddingModel string                  `mapstructure:"search_embedding_model"`
	Model                string                  `mapstructure:"model"`
	SystemPrompt         string                  `mapstructure:"system_prompt"`
	Timeout              int                     `mapstructure:"timeout"`
	SearchTimeout        int                     `mapstructure:"search_timeout"`
	LLMResolveTimeout    int                     `mapstructure:"llm_resolve_timeout"`
	LLMTimeout           int                     `mapstructure:"llm_timeout"`
	QdrantEnabled        bool                    `mapstructure:"qdrant_enabled"`
	QdrantHost           string                  `mapstructure:"qdrant_host"`
	QdrantPort           int                     `mapstructure:"qdrant_port"`
	QdrantAPIKey         string                  `mapstructure:"qdrant_api_key"`
	QdrantUseTLS         bool                    `mapstructure:"qdrant_use_tls"`
	QdrantCollection     string                  `mapstructure:"qdrant_collection"`
	QdrantScoreThreshold float64                 `mapstructure:"qdrant_score_threshold"`
	QdrantTTLHours       int                     `mapstructure:"qdrant_ttl_hours"`
	QdrantPoolSize       int                     `mapstructure:"qdrant_pool_size"`
	Theme                string                  `mapstructure:"theme"`
	Editor               string                  `mapstructure:"editor"`
	Commands             map[string]SlashCommand `mapstructure:"commands"`
}
