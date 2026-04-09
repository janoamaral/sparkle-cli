package ollama

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

func ParseStream(reader io.Reader, onChunk func(string) error) error {
	decoder := json.NewDecoder(reader)
	for {
		var chunk chatChunk
		if err := decoder.Decode(&chunk); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("decode ollama stream: %w", err)
		}

		if strings.TrimSpace(chunk.Error) != "" {
			return errors.New(chunk.Error)
		}

		if chunk.Message.Content != "" {
			if err := onChunk(chunk.Message.Content); err != nil {
				return err
			}
		}

		if chunk.Done {
			return nil
		}
	}
}
