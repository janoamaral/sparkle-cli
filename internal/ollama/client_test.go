package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamChatWithModelIncludesDefaultOptions(t *testing.T) {
	t.Parallel()

	requestBody := make(chan chatRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("request path = %q, want /api/chat", r.URL.Path)
		}

		defer func() { _ = r.Body.Close() }()

		var payload chatRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		requestBody <- payload

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"done\":true}\n"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "gemma3")
	err := client.StreamChatWithModel(context.Background(), "", []ChatMessage{{Role: "user", Content: "hola"}}, func(string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChatWithModel() error = %v", err)
	}

	payload := <-requestBody
	if payload.Model != "gemma3" {
		t.Fatalf("request model = %q, want gemma3", payload.Model)
	}
	if payload.Think {
		t.Fatalf("request think = %v, want false by default", payload.Think)
	}
	if !payload.Stream {
		t.Fatal("request stream = false, want true")
	}
	if payload.Options.Temperature != 1.0 {
		t.Fatalf("request temperature = %v, want 1.0", payload.Options.Temperature)
	}
	if payload.Options.TopP != 0.95 {
		t.Fatalf("request top_p = %v, want 0.95", payload.Options.TopP)
	}
	if payload.Options.TopK != 64 {
		t.Fatalf("request top_k = %d, want 64", payload.Options.TopK)
	}
	if len(payload.Messages) != 1 || payload.Messages[0].Content != "hola" {
		t.Fatalf("request messages = %+v, want single user message", payload.Messages)
	}
}

func TestStreamChatWithModelWithThinkingIncludesThinkFlag(t *testing.T) {
	t.Parallel()

	requestBody := make(chan chatRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("request path = %q, want /api/chat", r.URL.Path)
		}

		defer func() { _ = r.Body.Close() }()

		var payload chatRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		requestBody <- payload

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"done\":true}\n"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "qwen3")
	err := client.StreamChatWithModelWithThinking(context.Background(), "", []ChatMessage{{Role: "user", Content: "hola"}}, true, func(string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("StreamChatWithModelWithThinking() error = %v", err)
	}

	payload := <-requestBody
	if payload.Model != "qwen3" {
		t.Fatalf("request model = %q, want qwen3", payload.Model)
	}
	if !payload.Think {
		t.Fatalf("request think = %#v, want true", payload.Think)
	}
}
