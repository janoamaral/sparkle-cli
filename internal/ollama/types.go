package ollama

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type requestOptions struct {
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"top_p"`
	TopK        int     `json:"top_k"`
}

type chatRequest struct {
	Model    string         `json:"model"`
	Messages []ChatMessage  `json:"messages"`
	Options  requestOptions `json:"options"`
	Stream   bool           `json:"stream"`
}

type chatChunk struct {
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done  bool   `json:"done"`
	Error string `json:"error"`
}

type embedRequest struct {
	Model     string `json:"model"`
	Input     any    `json:"input,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	KeepAlive string `json:"keep_alive,omitempty"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Embedding  []float32   `json:"embedding"`
	Error      string      `json:"error"`
}

type modelOnlyRequest struct {
	Model string `json:"model"`
}
