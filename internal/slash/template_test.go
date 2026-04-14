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

func TestExpandKnownHyphenatedCommand(t *testing.T) {
	cfg := config.Config{Commands: map[string]config.SlashCommand{"generate-code": {Template: "Solo comando: {{.Input}}"}}}

	expanded, used, err := Expand("/generate-code listar archivos .go", cfg)
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if !used {
		t.Fatal("expected slash command to be used")
	}
	if expanded != "Solo comando: listar archivos .go" {
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

func TestExpandTranslateCommandUsesLanguageAndText(t *testing.T) {
	cfg := config.Config{Commands: map[string]config.SlashCommand{"translate": {Template: "Traduce al {{.Language}}: {{.Text}}"}}}

	expanded, used, err := Expand("/translate ingles Esto es una prueba", cfg)
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if !used {
		t.Fatal("expected slash command to be used")
	}
	if expanded != "Traduce al ingles: Esto es una prueba" {
		t.Fatalf("unexpected expansion: %s", expanded)
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
