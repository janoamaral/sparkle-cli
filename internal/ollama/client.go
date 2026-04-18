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
