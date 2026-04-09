package slash

import (
	"testing"

	"github.com/logico/sparkle-cli/internal/config"
)

func TestExpandKnownCommand(t *testing.T) {
	cfg := config.Config{Commands: map[string]config.SlashCommand{"fix": {Template: "Corrige: {{.Input}}"}}}

	expanded, used, err := Expand("/fix ls -la", cfg)
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if !used {
		t.Fatal("expected slash command to be used")
	}
	if expanded != "Corrige: ls -la" {
		t.Fatalf("unexpected expansion: %s", expanded)
	}
}

func TestExpandUnknownCommand(t *testing.T) {
	cfg := config.Config{Commands: map[string]config.SlashCommand{"fix": {Template: "Corrige: {{.Input}}"}}}

	_, _, err := Expand("/cheat grep", cfg)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExpandPassesThroughPlainInput(t *testing.T) {
	cfg := config.Config{}

	expanded, used, err := Expand("grep foo file.txt", cfg)
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if used {
		t.Fatal("expected plain input not to use slash command")
	}
	if expanded != "grep foo file.txt" {
		t.Fatalf("unexpected expansion: %s", expanded)
	}
}
