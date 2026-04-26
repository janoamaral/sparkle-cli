package ollama

import (
	"strings"
	"testing"
)

func TestParseStreamCollectsChunks(t *testing.T) {
	input := strings.NewReader(`{"message":{"content":"ls "},"done":false}
{"message":{"content":"-la"},"done":false}
{"done":true}
`)

	var builder strings.Builder
	err := ParseStream(input, func(chunk string) error {
		builder.WriteString(chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("ParseStream() error = %v", err)
	}
	if builder.String() != "ls -la" {
		t.Fatalf("unexpected stream content: %s", builder.String())
	}
}

func TestParseStreamReturnsChunkError(t *testing.T) {
	input := strings.NewReader("{\"error\":\"model missing\"}\n")

	err := ParseStream(input, func(chunk string) error { return nil })
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseStreamConvertsThinkingFieldToChannelMarkers(t *testing.T) {
	input := strings.NewReader(`{"message":{"thinking":"analizando"},"done":false}
{"message":{"content":"respuesta final"},"done":false}
{"done":true}
`)

	var builder strings.Builder
	err := ParseStream(input, func(chunk string) error {
		builder.WriteString(chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("ParseStream() error = %v", err)
	}

	want := "<|channel|>thought\nanalizando<channel|>respuesta final"
	if builder.String() != want {
		t.Fatalf("unexpected stream content: %q, want %q", builder.String(), want)
	}
}
