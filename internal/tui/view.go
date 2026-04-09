package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

var keyBindingTokens = []string{"󰘳+O", "󰘳+C", "󰌑", "󰌒", "󱊷"}

func (m model) View() string {
	status := m.renderTextWithKeyBindings(m.styles.status, m.status)
	if m.spinnerVisible {
		status = m.spinner.View() + " " + status
	}

	help := m.renderTextWithKeyBindings(m.styles.help, m.footerHelpText())
	body := lipgloss.JoinVertical(lipgloss.Left,
		m.viewport.View(),
		status,
		m.renderInputView(),
		help,
	)

	return m.styles.frame.Render(body)
}

func (m model) footerHelpText() string {
	return "󰌑 enviar · 󰌒 autocompleta · 󰘳+O aceptar · 󰘳+C cancelar/salir · 󱊷 salir · " + m.slashHelpText()
}

func (m model) slashHelpText() string {
	if len(m.cfg.Commands) == 0 {
		return "󰿠 sin slash commands"
	}

	commands := make([]string, 0, len(m.cfg.Commands))
	for name := range m.cfg.Commands {
		commands = append(commands, "/"+name)
	}
	sort.Strings(commands)

	return "󰿠 " + strings.Join(commands, " ")
}

func (m *model) refreshViewport() {
	parts := make([]string, 0, len(m.blocks))
	for _, block := range m.blocks {
		if strings.TrimSpace(block.rendered) == "" {
			continue
		}
		parts = append(parts, block.rendered)
	}
	m.viewport.SetContent(strings.Join(parts, "\n\n"))
	m.viewport.GotoBottom()
}

func (m *model) rebuildRenderer() {
	wrap := 100
	if m.viewport.Width > 0 {
		wrap = m.viewport.Width - 2
		if wrap < 20 {
			wrap = 20
		}
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(wrap),
	)
	if err != nil {
		return
	}
	m.renderer = renderer
	for index := range m.blocks {
		m.renderBlock(&m.blocks[index])
	}
}

func (m model) renderInputView() string {
	if !m.input.Focused() {
		return m.input.View()
	}

	command, _, ok := exactSlashCommand(m.input.Value(), m.cfg.Commands)
	if !ok {
		return m.input.View()
	}

	value := []rune(m.input.Value())
	position := m.input.Position()
	visibleValue, offset := visibleRunes(value, position, m.input.Width)
	visiblePos := position - offset
	if visiblePos < 0 {
		visiblePos = 0
	}
	if visiblePos > len(visibleValue) {
		visiblePos = len(visibleValue)
	}

	commandLength := len([]rune(command))
	before := m.renderInputSegment(visibleValue[:visiblePos], offset, commandLength)

	var body strings.Builder
	body.WriteString(before)

	if visiblePos < len(visibleValue) {
		cursorChar := string(visibleValue[visiblePos])
		if offset+visiblePos < commandLength {
			m.input.Cursor.TextStyle = m.styles.slashCommand
		} else {
			m.input.Cursor.TextStyle = m.input.TextStyle
		}
		m.input.Cursor.SetChar(cursorChar)
		body.WriteString(m.input.Cursor.View())
		body.WriteString(m.renderInputSegment(visibleValue[visiblePos+1:], offset+visiblePos+1, commandLength))
	} else {
		suggestion := []rune(m.input.CurrentSuggestion())
		if len(suggestion) > len(value) {
			m.input.Cursor.TextStyle = m.input.CompletionStyle
			m.input.Cursor.SetChar(string(suggestion[len(value)]))
			body.WriteString(m.input.Cursor.View())
			body.WriteString(m.input.CompletionStyle.Inline(true).Render(string(suggestion[len(value)+1:])))
		} else {
			m.input.Cursor.TextStyle = m.input.TextStyle
			m.input.Cursor.SetChar(" ")
			body.WriteString(m.input.Cursor.View())
		}
	}

	return m.input.PromptStyle.Render(m.input.Prompt) + body.String()
}

func (m model) renderInputSegment(segment []rune, start, commandLength int) string {
	if len(segment) == 0 {
		return ""
	}

	end := start + len(segment)
	text := string(segment)
	if end <= commandLength {
		return m.styles.slashCommand.Render(text)
	}
	if start >= commandLength {
		return text
	}

	split := commandLength - start
	return m.styles.slashCommand.Render(string(segment[:split])) + string(segment[split:])
}

func visibleRunes(value []rune, position, width int) ([]rune, int) {
	if width <= 0 || len(value) <= width {
		return value, 0
	}

	if position < 0 {
		position = 0
	}
	if position > len(value) {
		position = len(value)
	}

	offset := position - width
	if position < len(value) {
		offset++
	}
	if offset < 0 {
		offset = 0
	}
	if maxOffset := len(value) - width; offset > maxOffset {
		offset = maxOffset
	}

	return value[offset : offset+width], offset
}

func (m model) renderTextWithKeyBindings(base lipgloss.Style, value string) string {
	if value == "" {
		return ""
	}

	var body strings.Builder
	remaining := value
	for len(remaining) > 0 {
		index, token := nextKeyBindingToken(remaining)
		if index < 0 {
			body.WriteString(base.Render(remaining))
			break
		}

		if index > 0 {
			body.WriteString(base.Render(remaining[:index]))
		}
		body.WriteString(m.styles.keyBinding.Render(token))
		remaining = remaining[index+len(token):]
	}

	return body.String()
}

func nextKeyBindingToken(value string) (int, string) {
	bestIndex := -1
	bestToken := ""
	for _, token := range keyBindingTokens {
		index := strings.Index(value, token)
		if index < 0 {
			continue
		}
		if bestIndex == -1 || index < bestIndex {
			bestIndex = index
			bestToken = token
		}
	}

	return bestIndex, bestToken
}
