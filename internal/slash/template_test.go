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

func TestResolveSearchCommandMarksSearchKind(t *testing.T) {
	cfg := config.Config{Commands: map[string]config.SlashCommand{"search": {Template: "{{.Input}}", Kind: KindSearch}}}

	expansion, err := Resolve("/search como cambiar el prompt de sudo", cfg)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !expansion.Used {
		t.Fatal("expected slash command to be used")
	}
	if expansion.Kind != KindSearch {
		t.Fatalf("Resolve() kind = %q, want %q", expansion.Kind, KindSearch)
	}
	if expansion.Prompt != "como cambiar el prompt de sudo" {
		t.Fatalf("Resolve() prompt = %q, want payload", expansion.Prompt)
	}
}

func TestResolveSearchCommandKeepsOnlyQuestionPayload(t *testing.T) {
	cfg := config.Config{Commands: map[string]config.SlashCommand{"search": {Template: "{{.Input}}", Kind: KindSearch}}}

	expansion, err := Resolve("/search how to install ollama?", cfg)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if expansion.Prompt != "how to install ollama?" {
		t.Fatalf("Resolve() prompt = %q, want clean question payload", expansion.Prompt)
	}
}

func TestResolveNamedParamsWithPlaceholderPrompt(t *testing.T) {
	cfg := config.Config{Commands: map[string]config.SlashCommand{"ticket": {
		Prompt: "genera un ticket de Jira en el lenguaje {lang} a partir de la descripcion:\n{input}",
		System: "you are an expert software engineer that documents something",
		Params: []string{"lang"},
		Model:  "Gemma4",
	}}}

	expansion, err := Resolve("/ticket lang=en Agregar variables de entorno", cfg)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !expansion.Used {
		t.Fatal("expected slash command to be used")
	}
	if expansion.Model != "Gemma4" {
		t.Fatalf("Resolve() model = %q, want Gemma4", expansion.Model)
	}
	if expansion.SystemPrompt != "you are an expert software engineer that documents something" {
		t.Fatalf("Resolve() system prompt = %q, want command system prompt", expansion.SystemPrompt)
	}
	want := "genera un ticket de Jira en el lenguaje en a partir de la descripcion:\nAgregar variables de entorno"
	if expansion.Prompt != want {
		t.Fatalf("Resolve() prompt = %q, want %q", expansion.Prompt, want)
	}
}

func TestResolveNamedParamsRequiresConfiguredParams(t *testing.T) {
	cfg := config.Config{Commands: map[string]config.SlashCommand{"ticket": {
		Prompt: "ticket {lang}: {input}",
		Params: []string{"lang"},
	}}}

	_, err := Resolve("/ticket Agregar variables de entorno", cfg)
	if err == nil {
		t.Fatal("expected missing params error")
	}
	if got := err.Error(); got != "slash command /ticket requires params: lang" {
		t.Fatalf("Resolve() error = %q, want missing param message", got)
	}
}

func TestResolveConfigCommandWithoutPayload(t *testing.T) {
	cfg := config.Config{Commands: map[string]config.SlashCommand{"config": {Template: "{{.Input}}", Kind: KindConfig}}}

	expansion, err := Resolve("/config", cfg)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !expansion.Used {
		t.Fatal("expected slash command to be used")
	}
	if expansion.Kind != KindConfig {
		t.Fatalf("Resolve() kind = %q, want %q", expansion.Kind, KindConfig)
	}
	if expansion.Prompt != "" {
		t.Fatalf("Resolve() prompt = %q, want empty prompt", expansion.Prompt)
	}
}

func TestResolveConfigCommandRejectsPayload(t *testing.T) {
	cfg := config.Config{Commands: map[string]config.SlashCommand{"config": {Template: "{{.Input}}", Kind: KindConfig}}}

	_, err := Resolve("/config ahora", cfg)
	if err == nil {
		t.Fatal("expected payload validation error")
	}
	if got := err.Error(); got != "slash command /config does not accept input" {
		t.Fatalf("Resolve() error = %q, want payload error", got)
	}
}
