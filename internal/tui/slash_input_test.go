package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/logico/sparkle-cli/internal/config"
)

const fixTemplate = "fix {{.Input}}"
const assistantResponse = "respuesta final"

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
	m.viewport.Width = 30

	colored := m.renderUserBlockContent("/fix ls -la")
	userSlash := m.styles.slashCommand.Background(lipgloss.Color(m.colors.bgBase)).Render("/fix")
	if !strings.Contains(colored, userSlash) {
		t.Fatalf("renderUserBlockContent() = %q, want slash command highlight", colored)
	}
	if !strings.Contains(colored, m.styles.userText.Render("ls -la")) {
		t.Fatalf("renderUserBlockContent() = %q, want remainder text", colored)
	}

	plain := m.renderUserBlockContent("/fi ls -la")
	if !strings.Contains(plain, m.styles.userText.Render("/fi ls -la")) {
		t.Fatalf("renderUserBlockContent() = %q, want plain user text", plain)
	}
}

func TestRenderTextWithKeyBindings(t *testing.T) {
	m := newModel(config.Config{}, "")
	rendered := m.renderTextWithKeyBindings(m.styles.help, "󰘳+O aceptar · 󰘳+Y copiar · 󱊷 salir")

	if !strings.Contains(rendered, m.styles.keyBinding.Render("󰘳+O")) {
		t.Fatalf("renderTextWithKeyBindings() did not highlight ctrl+o: %q", rendered)
	}
	if !strings.Contains(rendered, m.styles.keyBinding.Render("󰘳+Y")) {
		t.Fatalf("renderTextWithKeyBindings() did not highlight ctrl+y: %q", rendered)
	}
	if !strings.Contains(rendered, m.styles.keyBinding.Render("󱊷")) {
		t.Fatalf("renderTextWithKeyBindings() did not highlight esc: %q", rendered)
	}
	if !strings.Contains(rendered, m.styles.help.Render(" aceptar · ")) {
		t.Fatalf("renderTextWithKeyBindings() did not preserve help style between shortcuts: %q", rendered)
	}
}

func TestHandleKeyMsgCopiesLastAssistantToClipboard(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.blocks = []messageBlock{{role: "assistant", raw: assistantResponse, rendered: assistantResponse}}
	m.activeBlockIndex = 0

	var copied string
	m.clipboardWrite = func(value string) error {
		copied = value
		return nil
	}

	handled, cmd := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlY})

	if !handled {
		t.Fatal("handleKeyMsg() should handle ctrl+y")
	}
	if cmd != nil {
		t.Fatalf("handleKeyMsg() cmd = %v, want nil", cmd)
	}
	if copied != assistantResponse {
		t.Fatalf("clipboard content = %q, want %s", copied, assistantResponse)
	}
	if m.status != "Respuesta copiada al clipboard." {
		t.Fatalf("status = %q, want copy confirmation", m.status)
	}
}

func TestHandleKeyMsgCopyWithoutAssistantResponse(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.clipboardWrite = func(value string) error {
		t.Fatalf("clipboardWrite(%q) should not be called", value)
		return nil
	}

	handled, cmd := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlY})

	if !handled {
		t.Fatal("handleKeyMsg() should handle ctrl+y")
	}
	if cmd != nil {
		t.Fatalf("handleKeyMsg() cmd = %v, want nil", cmd)
	}
	if m.status != "No hay respuesta para copiar todavia." {
		t.Fatalf("status = %q, want missing response message", m.status)
	}
}

func TestConversationContentAddsSeparatorAfterAssistantBlock(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 12
	m.blocks = []messageBlock{
		{role: "user", rendered: "pregunta"},
		{role: "assistant", rendered: "respuesta"},
	}

	got := m.conversationContent()
	separator := m.separatorLine()

	if !strings.Contains(got, "pregunta\nrespuesta") {
		t.Fatalf("conversationContent() = %q, want user message followed by assistant message", got)
	}
	if lipgloss.Width(separator) != 12 {
		t.Fatalf("separator width = %d, want 12", lipgloss.Width(separator))
	}
	if strings.Contains(got, separator) {
		t.Fatalf("conversationContent() = %q, want no separators in the updated layout", got)
	}
}

func TestRenderBlockHeaderIsHiddenForConversationBlocks(t *testing.T) {
	m := newModel(config.Config{}, "")
	if got := m.renderBlockHeader("user"); got != "" {
		t.Fatalf("renderBlockHeader(user) = %q, want empty", got)
	}
	if got := m.renderBlockHeader("assistant"); got != "" {
		t.Fatalf("renderBlockHeader(assistant) = %q, want empty", got)
	}
}

func TestRenderInputViewWrapsLongParagraph(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 16
	m.input.Width = m.inputContentWidth()
	m.input.SetValue("esta pregunta es bastante larga para una sola linea")
	m.input.CursorEnd()

	rendered := m.renderInputView()
	lines := strings.Split(rendered, "\n")

	if len(lines) < 2 {
		t.Fatalf("renderInputView() = %q, want wrapped lines", rendered)
	}
	for _, line := range lines {
		if lipgloss.Width(line) > m.inputContentWidth() {
			t.Fatalf("renderInputView() line width = %d, want <= %d in %q", lipgloss.Width(line), m.inputContentWidth(), line)
		}
	}
}

func TestRenderInputViewOmitsPromptPrefix(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 24
	m.input.SetValue("are you there?")
	m.input.CursorEnd()

	rendered := m.renderInputView()
	if strings.Contains(rendered, "> ") {
		t.Fatalf("renderInputView() = %q, want no prompt prefix", rendered)
	}
	if !strings.Contains(rendered, m.input.TextStyle.Render("are you there?")) {
		t.Fatalf("renderInputView() = %q, want input text rendered with input background style", rendered)
	}
}

func TestViewDoesNotRenderHeaderAndRestoresHelpFooter(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 30
	m.viewport.Height = 8
	m.refreshViewport()

	rendered := m.View()
	if strings.Contains(rendered, "# sparkle-cli") {
		t.Fatalf("View() = %q, want header hidden", rendered)
	}
	if !strings.Contains(rendered, "Enter enviar") {
		t.Fatalf("View() = %q, want footer help visible", rendered)
	}
}

func TestViewFillsWindowWidthWithoutRightGap(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.handleWindowSize(tea.WindowSizeMsg{Width: 40, Height: 12})

	rendered := m.View()
	for _, line := range strings.Split(rendered, "\n") {
		if lipgloss.Width(line) != 40 {
			t.Fatalf("View() line width = %d, want 40 in %q", lipgloss.Width(line), line)
		}
	}
}

func TestFillLinesWithBackgroundPadsTrailingColumns(t *testing.T) {
	m := newModel(config.Config{}, "")
	got := m.fillLinesWithBackground("hola", 6, m.colors.bgBase)
	wantSuffix := lipgloss.NewStyle().Background(lipgloss.Color(m.colors.bgBase)).Render("  ")

	if !strings.HasSuffix(got, wantSuffix) {
		t.Fatalf("fillLinesWithBackground() = %q, want trailing columns painted with base background", got)
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
		userSlash := m.styles.slashCommand.Background(lipgloss.Color(m.colors.bgBase)).Render("/fix")
		if !strings.Contains(rendered, userSlash) {
			t.Fatalf("renderUserBlockContent() = %q, want slash command highlight", rendered)
		}
	}
	for _, line := range lines {
		maxWidth := m.contentWidth() + m.styles.userBlock.GetHorizontalFrameSize()
		if lipgloss.Width(line) > maxWidth {
			t.Fatalf("renderUserBlockContent() line width = %d, want <= %d in %q", lipgloss.Width(line), maxWidth, line)
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
	if strings.Contains(m.conversationContent(), "assistant") {
		t.Fatalf("conversationContent() = %q, want no assistant label before first chunk", m.conversationContent())
	}

	if m.cancel != nil {
		m.cancel()
	}
}

func TestStripANSIBackgroundCodesPreservesResetSequences(t *testing.T) {
	input := "\x1b[38;5;252;48;5;235mcode\x1b[0m  padding"

	got := stripANSIBackgroundCodes(input)

	if strings.Contains(got, "48;5;235") {
		t.Fatalf("stripANSIBackgroundCodes() = %q, want background color removed", got)
	}
	if !strings.Contains(got, "\x1b[0m") {
		t.Fatalf("stripANSIBackgroundCodes() = %q, want reset sequence preserved to avoid background bleed", got)
	}
}

func TestStripANSIBackgroundCodesTreatsEmptySGRAsReset(t *testing.T) {
	input := "\x1b[48;5;235mcode\x1b[m  padding"

	got := stripANSIBackgroundCodes(input)

	if strings.Contains(got, "48;5;235") {
		t.Fatalf("stripANSIBackgroundCodes() = %q, want background color removed", got)
	}
	if !strings.Contains(got, "\x1b[0m") {
		t.Fatalf("stripANSIBackgroundCodes() = %q, want empty reset preserved as 0m", got)
	}
}

func TestAssistantBlockReappliesBaseBackgroundAfterReset(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 40

	content := "\x1b[38;5;252mtexto\x1b[0m final"
	rendered := m.renderAssistantWithBaseBackground(content)

	if !strings.Contains(rendered, "48;2;24;24;24") {
		t.Fatalf("renderAssistantWithBaseBackground() = %q, want base background sequence for #181818", rendered)
	}
	if !strings.Contains(rendered, "\x1b[0;48;2;24;24;24m") {
		t.Fatalf("renderAssistantWithBaseBackground() = %q, want background reapplied immediately after reset", rendered)
	}
}

func TestAssistantBlockKeepsBaseBackgroundAcrossLineBoundaries(t *testing.T) {
	m := newModel(config.Config{}, "")
	rendered := m.renderAssistantWithBaseBackground("linea uno\nlinea dos")

	want := "\x1b[0;48;2;24;24;24m"
	if !strings.HasPrefix(rendered, want) {
		t.Fatalf("renderAssistantWithBaseBackground() = %q, want base background prefix at line start", rendered)
	}
	if !strings.Contains(rendered, want+"\n"+want) {
		t.Fatalf("renderAssistantWithBaseBackground() = %q, want base background preserved across newlines", rendered)
	}
}
