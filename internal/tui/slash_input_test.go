package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/logico/sparkle-cli/internal/config"
)

const fixTemplate = "fix {{.Input}}"

func TestSlashCommandSuggestionsSorted(t *testing.T) {
	commands := map[string]config.SlashCommand{
		"fix":           {Template: fixTemplate},
		"explain":       {Template: "explain {{.Input}}"},
		"generate-code": {Template: "generate {{.Input}}"},
	}

	got := slashCommandSuggestions(commands)
	want := []string{"/explain ", "/fix ", "/generate-code "}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("slashCommandSuggestions() = %v, want %v", got, want)
	}
}

func TestExactSlashCommand(t *testing.T) {
	commands := map[string]config.SlashCommand{
		"fix": {Template: fixTemplate},
	}

	tests := []struct {
		name      string
		input     string
		wantCmd   string
		wantRest  string
		wantMatch bool
	}{
		{name: "exact command", input: "/fix", wantCmd: "/fix", wantMatch: true},
		{name: "command with payload", input: "/fix ls -la", wantCmd: "/fix", wantRest: " ls -la", wantMatch: true},
		{name: "command with trailing space", input: "/fix ", wantCmd: "/fix", wantRest: " ", wantMatch: true},
		{name: "partial command", input: "/fi", wantMatch: false},
		{name: "unknown command", input: "/foo ls", wantMatch: false},
		{name: "plain text", input: "ls -la", wantMatch: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotCmd, gotRest, gotMatch := exactSlashCommand(test.input, commands)
			if gotCmd != test.wantCmd || gotRest != test.wantRest || gotMatch != test.wantMatch {
				t.Fatalf("exactSlashCommand(%q) = (%q, %q, %t), want (%q, %q, %t)", test.input, gotCmd, gotRest, gotMatch, test.wantCmd, test.wantRest, test.wantMatch)
			}
		})
	}
}

func TestRenderUserBlockContentOnlyColorsKnownSlashCommands(t *testing.T) {
	cfg := config.Config{Commands: map[string]config.SlashCommand{"fix": {Template: fixTemplate}}}
	m := newModel(cfg, "")

	colored := m.renderUserBlockContent("/fix ls -la")
	wantColored := m.styles.slashCommand.Render("/fix") + " " + m.styles.userText.Render("ls -la")
	if colored != wantColored {
		t.Fatalf("renderUserBlockContent() = %q, want %q", colored, wantColored)
	}

	plain := m.renderUserBlockContent("/fi ls -la")
	wantPlain := m.styles.userText.Render("/fi ls -la")
	if plain != wantPlain {
		t.Fatalf("renderUserBlockContent() = %q, want %q", plain, wantPlain)
	}
}

func TestRenderTextWithKeyBindings(t *testing.T) {
	m := newModel(config.Config{}, "")
	rendered := m.renderTextWithKeyBindings(m.styles.help, "󰘳+O aceptar · 󱊷 salir")

	if !strings.Contains(rendered, m.styles.keyBinding.Render("󰘳+O")) {
		t.Fatalf("renderTextWithKeyBindings() did not highlight ctrl+o: %q", rendered)
	}
	if !strings.Contains(rendered, m.styles.keyBinding.Render("󱊷")) {
		t.Fatalf("renderTextWithKeyBindings() did not highlight esc: %q", rendered)
	}
	if !strings.Contains(rendered, m.styles.help.Render(" aceptar · ")) {
		t.Fatalf("renderTextWithKeyBindings() did not preserve help style between shortcuts: %q", rendered)
	}
}

func TestConversationContentAddsSeparatorAfterEachBlock(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 12
	m.blocks = []messageBlock{
		{role: "user", rendered: "pregunta"},
		{role: "assistant", rendered: "respuesta"},
	}

	got := m.conversationContent()
	separator := m.separatorLine()

	if !strings.Contains(got, "pregunta\n"+separator+"\nrespuesta") {
		t.Fatalf("conversationContent() = %q, want separator between user and assistant blocks", got)
	}
	if !strings.Contains(got, "respuesta\n"+separator) {
		t.Fatalf("conversationContent() = %q, want assistant separator after response", got)
	}
	if !strings.HasSuffix(got, separator) {
		t.Fatalf("conversationContent() = %q, want trailing separator", got)
	}
	if lipgloss.Width(separator) != 12 {
		t.Fatalf("separator width = %d, want 12", lipgloss.Width(separator))
	}
	if strings.Contains(got, "\n\n") {
		t.Fatalf("conversationContent() = %q, got blank line between blocks", got)
	}
}

func TestRenderInputViewWrapsLongParagraph(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.width = 18
	m.viewport.Width = 16
	m.input.SetValue("esta pregunta es bastante larga para una sola linea")
	m.input.CursorEnd()

	rendered := m.renderInputView()
	lines := strings.Split(rendered, "\n")

	if len(lines) < 2 {
		t.Fatalf("renderInputView() = %q, want wrapped lines", rendered)
	}
	for _, line := range lines {
		if lipgloss.Width(line) > 16 {
			t.Fatalf("renderInputView() line width = %d, want <= 16 in %q", lipgloss.Width(line), line)
		}
	}
}

func TestRenderUserBlockContentWrapsLongQuestion(t *testing.T) {
	cfg := config.Config{Commands: map[string]config.SlashCommand{"fix": {Template: fixTemplate}}}
	m := newModel(cfg, "")
	m.viewport.Width = 14

	rendered := m.renderUserBlockContent("/fix explica este comando con bastante detalle")
	lines := strings.Split(rendered, "\n")

	if len(lines) < 2 {
		t.Fatalf("renderUserBlockContent() = %q, want wrapped lines", rendered)
	}
	if !strings.Contains(rendered, m.styles.slashCommand.Render("/fix")) {
		t.Fatalf("renderUserBlockContent() = %q, want slash command highlight", rendered)
	}
	for _, line := range lines {
		if lipgloss.Width(line) > 14 {
			t.Fatalf("renderUserBlockContent() line width = %d, want <= 14 in %q", lipgloss.Width(line), line)
		}
	}
}

func TestHandleStreamChunkCreatesAssistantBlockLazily(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.blocks = []messageBlock{{role: "user", raw: "hola", rendered: "hola"}}
	m.activeBlockIndex = -1
	streamCh := make(chan streamEvent)
	m.streamCh = streamCh

	cmd := m.handleStreamChunk(streamChunkMsg{content: "respuesta"})

	if cmd == nil {
		t.Fatal("handleStreamChunk() returned nil cmd")
	}
	if len(m.blocks) != 2 {
		t.Fatalf("blocks len = %d, want 2", len(m.blocks))
	}
	if m.blocks[1].role != "assistant" {
		t.Fatalf("assistant role = %q, want assistant", m.blocks[1].role)
	}
	if m.blocks[1].raw != "respuesta" {
		t.Fatalf("assistant raw = %q, want respuesta", m.blocks[1].raw)
	}
	if m.activeBlockIndex != 1 {
		t.Fatalf("activeBlockIndex = %d, want 1", m.activeBlockIndex)
	}
	close(streamCh)
}

func TestStartRequestDoesNotRenderEmptyAssistantBlock(t *testing.T) {
	m := newModel(config.Config{Timeout: 0}, "")
	cmd := m.startRequest("Estás ahí?")

	if cmd == nil {
		t.Fatal("startRequest() returned nil cmd")
	}
	if len(m.blocks) != 1 {
		t.Fatalf("blocks len = %d, want 1", len(m.blocks))
	}
	if m.blocks[0].role != "user" {
		t.Fatalf("first block role = %q, want user", m.blocks[0].role)
	}
	if m.activeBlockIndex != -1 {
		t.Fatalf("activeBlockIndex = %d, want -1 before first chunk", m.activeBlockIndex)
	}
	if strings.Contains(m.conversationContent(), "") {
		t.Fatalf("conversationContent() = %q, want no assistant header before first chunk", m.conversationContent())
	}

	if m.cancel != nil {
		m.cancel()
	}
}
