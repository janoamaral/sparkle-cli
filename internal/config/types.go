package config

type SlashCommand struct {
	Template string   `mapstructure:"template"`
	Prompt   string   `mapstructure:"prompt"`
	System   string   `mapstructure:"system"`
	Params   []string `mapstructure:"params"`
	Optional []string `mapstructure:"optional_params"`
	Model    string   `mapstructure:"model"`
	Kind     string   `mapstructure:"kind"`
	Desc     string   `mapstructure:"desc"`
}

type Config struct {
	OllamaURL            string                  `mapstructure:"ollama_url"`
	SearchURL            string                  `mapstructure:"search_url"`
	SearchEmbeddingModel string                  `mapstructure:"search_embedding_model"`
	SearchQueryModel     string                  `mapstructure:"search_query_model"`
	AgentFunctionModel   string                  `mapstructure:"agent_function_model"`
	AgentLuaDir          string                  `mapstructure:"agent_lua_dir"`
	AgentSkillsDir       string                  `mapstructure:"agent_skills_dir"`
	AgentMaxIterations   int                     `mapstructure:"agent_max_iterations"`
	AgentTimeoutSeconds  int                     `mapstructure:"agent_timeout_seconds"`
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
	QdrantLookupLimit    int                     `mapstructure:"qdrant_lookup_limit"`
	QdrantMinRerankScore float64                 `mapstructure:"qdrant_min_rerank_score"`
	QdrantLexicalWeight  float64                 `mapstructure:"qdrant_lexical_weight"`
	QdrantSemanticWeight float64                 `mapstructure:"qdrant_semantic_weight"`
	Theme                string                  `mapstructure:"theme"`
	Logs                 bool                    `mapstructure:"logs"`
	Profiler             bool                    `mapstructure:"profiler"`
	Editor               string                  `mapstructure:"editor"`
	SlashCommandsFile    string                  `mapstructure:"slash_commands_file"`
	SlashCommandsDir     string                  `mapstructure:"slash_commands_dir"`
	Commands             map[string]SlashCommand `mapstructure:"commands"`
}
