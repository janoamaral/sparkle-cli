package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	baseURL string
	model   string
	http    *http.Client
}

var defaultRequestOptions = requestOptions{
	Temperature: 1.0,
	TopP:        0.95,
	TopK:        64,
}

func NewClient(baseURL, model string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		http:    &http.Client{},
	}
}

func (c *Client) StreamChat(ctx context.Context, messages []ChatMessage, onChunk func(string) error) error {
	return c.StreamChatWithModel(ctx, c.model, messages, onChunk)
}

func (c *Client) PreloadModel(ctx context.Context, model string) error {
	trimmedModel := strings.TrimSpace(model)
	if trimmedModel == "" {
		trimmedModel = c.model
	}
	if strings.TrimSpace(trimmedModel) == "" {
		return nil
	}

	requestBody, err := marshalModelOnlyRequest(modelOnlyRequest{Model: trimmedModel})
	if err != nil {
		return err
	}

	if err := c.doModelOnlyRequest(ctx, c.baseURL+"/api/chat", requestBody); err == nil {
		return nil
	}

	return c.doModelOnlyRequest(ctx, c.baseURL+"/api/generate", requestBody)
}

func (c *Client) ChatWithModel(ctx context.Context, model string, messages []ChatMessage) (string, error) {
	var builder strings.Builder
	err := c.StreamChatWithModel(ctx, model, messages, func(chunk string) error {
		builder.WriteString(chunk)
		return nil
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(builder.String()), nil
}

func (c *Client) StreamChatWithModel(ctx context.Context, model string, messages []ChatMessage, onChunk func(string) error) error {
	if strings.TrimSpace(model) == "" {
		model = c.model
	}

	body, err := marshalRequest(chatRequest{
		Model:    model,
		Messages: messages,
		Options:  defaultRequestOptions,
		Stream:   true,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if readErr != nil {
			return fmt.Errorf("ollama status %d", resp.StatusCode)
		}
		message := strings.TrimSpace(string(payload))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("ollama status %d: %s", resp.StatusCode, message)
	}

	return ParseStream(resp.Body, onChunk)
}

func marshalRequest(request chatRequest) ([]byte, error) {
	buffer := bytes.NewBuffer(nil)
	encoder := json.NewEncoder(buffer)
	if err := encoder.Encode(request); err != nil {
		return nil, fmt.Errorf("encode ollama request: %w", err)
	}
	return bytes.TrimSpace(buffer.Bytes()), nil
}

func marshalModelOnlyRequest(request modelOnlyRequest) ([]byte, error) {
	buffer := bytes.NewBuffer(nil)
	encoder := json.NewEncoder(buffer)
	if err := encoder.Encode(request); err != nil {
		return nil, fmt.Errorf("encode ollama preload request: %w", err)
	}
	return bytes.TrimSpace(buffer.Bytes()), nil
}

func (c *Client) doModelOnlyRequest(ctx context.Context, endpoint string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ollama preload request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request ollama preload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if readErr != nil {
			return fmt.Errorf("ollama preload status %d", resp.StatusCode)
		}
		message := strings.TrimSpace(string(payload))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("ollama preload status %d: %s", resp.StatusCode, message)
	}

	return nil
}

func (c *Client) EmbedWithModel(ctx context.Context, model string, input []string) ([][]float32, error) {
	trimmedInputs := normalizeEmbedInputs(input)
	if len(trimmedInputs) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(model) == "" {
		model = c.model
	}

	request := embedRequest{Model: model, KeepAlive: "5m"}
	if len(trimmedInputs) == 1 {
		request.Input = trimmedInputs[0]
	} else {
		request.Input = trimmedInputs
	}
	body, err := marshalEmbedRequest(request)
	if err != nil {
		return nil, err
	}

	response, statusCode, err := c.doEmbedRequest(ctx, c.baseURL+"/api/embed", body)
	if err == nil {
		return normalizeEmbedResponse(response, len(trimmedInputs))
	}
	if statusCode != http.StatusNotFound {
		return nil, err
	}
	return c.embedWithLegacyEndpoint(ctx, model, trimmedInputs)
}

func normalizeEmbedInputs(input []string) []string {
	trimmedInputs := make([]string, 0, len(input))
	for _, current := range input {
		trimmed := strings.TrimSpace(current)
		if trimmed != "" {
			trimmedInputs = append(trimmedInputs, trimmed)
		}
	}
	return trimmedInputs
}

func (c *Client) embedWithLegacyEndpoint(ctx context.Context, model string, input []string) ([][]float32, error) {
	embeddings := make([][]float32, 0, len(input))
	for _, current := range input {
		legacyBody, legacyErr := marshalEmbedRequest(embedRequest{Model: model, Prompt: current, KeepAlive: "5m"})
		if legacyErr != nil {
			return nil, legacyErr
		}
		legacyResponse, _, requestErr := c.doEmbedRequest(ctx, c.baseURL+"/api/embeddings", legacyBody)
		if requestErr != nil {
			return nil, requestErr
		}
		vectors, normalizeErr := normalizeEmbedResponse(legacyResponse, 1)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		embeddings = append(embeddings, vectors[0])
	}

	return embeddings, nil
}

func marshalEmbedRequest(request embedRequest) ([]byte, error) {
	buffer := bytes.NewBuffer(nil)
	encoder := json.NewEncoder(buffer)
	if err := encoder.Encode(request); err != nil {
		return nil, fmt.Errorf("encode ollama embed request: %w", err)
	}
	return bytes.TrimSpace(buffer.Bytes()), nil
}

func (c *Client) doEmbedRequest(ctx context.Context, endpoint string, body []byte) (embedResponse, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return embedResponse{}, 0, fmt.Errorf("create ollama embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return embedResponse{}, 0, fmt.Errorf("request ollama embeddings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if readErr != nil {
			return embedResponse{}, resp.StatusCode, fmt.Errorf("ollama embeddings status %d", resp.StatusCode)
		}
		message := strings.TrimSpace(string(payload))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return embedResponse{}, resp.StatusCode, fmt.Errorf("ollama embeddings status %d: %s", resp.StatusCode, message)
	}

	var decoded embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return embedResponse{}, resp.StatusCode, fmt.Errorf("decode ollama embeddings response: %w", err)
	}
	if strings.TrimSpace(decoded.Error) != "" {
		return embedResponse{}, resp.StatusCode, fmt.Errorf("ollama embeddings error: %s", strings.TrimSpace(decoded.Error))
	}

	return decoded, resp.StatusCode, nil
}

func normalizeEmbedResponse(response embedResponse, expected int) ([][]float32, error) {
	if len(response.Embeddings) > 0 {
		return response.Embeddings, nil
	}
	if len(response.Embedding) > 0 {
		return [][]float32{response.Embedding}, nil
	}
	if expected == 0 {
		return nil, nil
	}
	return nil, fmt.Errorf("ollama embeddings response did not contain vectors")
}
