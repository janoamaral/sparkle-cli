package ollama

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

func ParseStream(reader io.Reader, onChunk func(string) error) error {
	_, err := ParseStreamWithStats(reader, onChunk)
	return err
}

func ParseStreamWithStats(reader io.Reader, onChunk func(string) error) (StreamStats, error) {
	decoder := json.NewDecoder(reader)
	thinkingOpen := false
	stats := StreamStats{}
	for {
		var chunk chatChunk
		if err := decoder.Decode(&chunk); err != nil {
			if errors.Is(err, io.EOF) {
				return stats, nil
			}
			return stats, fmt.Errorf("decode ollama stream: %w", err)
		}

		if strings.TrimSpace(chunk.Error) != "" {
			return stats, errors.New(chunk.Error)
		}

		if chunk.Message.Thinking != "" {
			if !thinkingOpen {
				if err := onChunk("<|channel|>thought\n"); err != nil {
					return stats, err
				}
				thinkingOpen = true
			}
			if err := onChunk(chunk.Message.Thinking); err != nil {
				return stats, err
			}
		}

		if chunk.Message.Content != "" {
			if thinkingOpen {
				if err := onChunk("<channel|>"); err != nil {
					return stats, err
				}
				thinkingOpen = false
			}
			if err := onChunk(chunk.Message.Content); err != nil {
				return stats, err
			}
		}

		if chunk.Done {
			stats.PromptEvalCount = chunk.PromptEvalCount
			stats.EvalCount = chunk.EvalCount
			if thinkingOpen {
				if err := onChunk("<channel|>"); err != nil {
					return stats, err
				}
			}
			return stats, nil
		}
	}
}
