package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wrap"
)

var keyBindingTokens = []string{"Ctrl+E", "Ctrl+O", "Ctrl+Y", "Ctrl+T", "Ctrl+C", "Enter", "Tab", "Esc", "/", "󰘳+E", "󰘳+O", "󰘳+Y", "󰘳+T", "󰘳+C", "󰌑", "󰌒", "󱊷", ""}

func (m model) View() string {
	conversationBody := m.fillLinesWithBackground(m.conversationViewportView(), m.outerWidth(), m.colors.bgBase)
	conversation := m.styles.conversation.Width(m.outerWidth()).Render(conversationBody)
	inputBody := m.fillLinesWithBackground(m.renderInputView(), m.inputContentWidth(), m.colors.bgRaised)
	input := m.styles.inputBox.Width(m.outerWidth()).Render(inputBody)
	help := m.renderFooterHelp()

	sections := []string{conversation}
	if status := m.renderStatusLine(); status != "" {
		sections = append(sections, status)
	}
	sections = append(sections, input, help)
	body := lipgloss.JoinVertical(lipgloss.Left, sections...)
	view := m.styles.frame.Render(body)

	if m.width > 0 && m.height > 0 {
		view = lipgloss.Place(
			m.width,
			m.height,
			lipgloss.Left,
			lipgloss.Top,
			view,
			lipgloss.WithWhitespaceChars(" "),
			lipgloss.WithWhitespaceBackground(lipgloss.Color(m.colors.bgBase)),
		)
	} else {
		view = lipgloss.NewStyle().Background(lipgloss.Color(m.colors.bgBase)).Render(view)
	}

	return m.renderAssistantWithBaseBackground(view)
}

func (m model) conversationViewportView() string {
	if m.viewport.Height <= 0 || m.viewport.Width <= 0 {
		return ""
	}
	return m.viewport.View()
}

func (m model) renderStatusLine() string {
	if m.status == "" || m.status == "Listo para recibir mensajes" || m.status == "Ctrl+E abre editor del input · Ctrl+O inserta en buffer · Ctrl+Y copia al clipboard · Enter envia otra consulta." {
		return ""
	}

	status := m.status
	prefix := "•"
	if m.spinnerVisible {
		prefix = stripANSISequences(m.spinner.View())
	}

	spinnerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#3fa266")).Background(lipgloss.Color(m.colors.bgBase))
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.colors.status)).Background(lipgloss.Color(m.colors.bgBase))
	spaceStyle := lipgloss.NewStyle().Background(lipgloss.Color(m.colors.bgBase))
	line := spinnerStyle.Render(prefix) + spaceStyle.Render(" ") + statusStyle.Render(status)
	return lipgloss.NewStyle().Background(lipgloss.Color(m.colors.bgBase)).Width(m.outerWidth()).Render(line)
}

func (m model) footerHelpText() string {
	shortcuts := "Enter enviar · Tab autocompleta · Ctrl+T modo · Ctrl+E editar input · Ctrl+O aceptar · Ctrl+Y copiar · Ctrl+C cancelar/salir · Esc salir"
	return shortcuts + "\n" + strings.TrimLeft(m.slashHelpText(), " ")
}

func (m model) renderFooterHelp() string {
	lines := strings.Split(m.footerHelpText(), "\n")
	renderedLines := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimLeft(line, " ")
		wrapped := m.wrapParagraph(m.renderTextWithKeyBindings(m.styles.help, line), m.outerWidth())
		body := m.fillLinesWithBackground(wrapped, m.outerWidth(), m.colors.bgBase)
		renderedLines = append(renderedLines, m.styles.help.Width(m.outerWidth()).Align(lipgloss.Left).Render(body))
	}
	return lipgloss.JoinVertical(lipgloss.Left, renderedLines...)
}

func (m model) slashHelpText() string {
	if len(m.cfg.Commands) == 0 {
		return "sin slash commands"
	}

	commands := make([]string, 0, len(m.cfg.Commands))
	for name := range m.cfg.Commands {
		commands = append(commands, "/"+name)
	}
	sort.Strings(commands)

	return strings.Join(commands, " ")
}

func (m *model) refreshViewport() {
	m.viewport.SetContent(m.conversationContent())
	m.viewport.GotoBottom()
}

func (m model) conversationContent() string {
	visibleBlocks := make([]messageBlock, 0, len(m.blocks))
	for _, block := range m.blocks {
		if strings.TrimSpace(block.rendered) == "" {
			continue
		}
		visibleBlocks = append(visibleBlocks, block)
	}
	if len(visibleBlocks) == 0 {
		return ""
	}

	var body strings.Builder
	for index, block := range visibleBlocks {
		if index > 0 {
			body.WriteString("\n")
		}

		body.WriteString(block.rendered)
	}

	return body.String()
}

func (m model) shouldRenderSeparatorAfter(role string) bool {
	return false
}

func (m model) separatorLine() string {
	width := m.contentWidth()
	if width <= 0 {
		width = 1
	}
	return m.styles.separator.Render(strings.Repeat("─", width))
}

func (m *model) rebuildRenderer() {
	wrap := 100
	if m.viewport.Width > 0 {
		wrap = m.viewport.Width
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
	value := []rune(m.input.Value())
	position := m.input.Position()
	if position < 0 {
		position = 0
	}
	if position > len(value) {
		position = len(value)
	}

	commandLength := 0
	if command, _, ok := exactSlashCommand(m.input.Value(), m.cfg.Commands); ok {
		commandLength = len([]rune(command))
	}

	before := m.renderInputSegment(value[:position], 0, commandLength)

	var body strings.Builder
	body.WriteString(before)

	if position < len(value) {
		cursorChar := string(value[position])
		if position < commandLength {
			m.input.Cursor.TextStyle = m.styles.slashCommand
		} else {
			m.input.Cursor.TextStyle = m.input.TextStyle
		}
		m.input.Cursor.SetChar(cursorChar)
		body.WriteString(m.input.Cursor.View())
		body.WriteString(m.renderInputSegment(value[position+1:], position+1, commandLength))
	} else if m.input.Focused() {
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
	} else if len(value) == 0 && m.input.Placeholder != "" {
		body.WriteString(m.input.PlaceholderStyle.Inline(true).Render(m.input.Placeholder))
	}

	inputLine := m.wrapParagraph(m.input.PromptStyle.Render(m.input.Prompt)+body.String(), m.inputContentWidth())
	indicator := m.renderModeIndicator()
	if indicator == "" {
		return inputLine
	}
	if strings.TrimSpace(inputLine) == "" {
		return "\n\n" + indicator
	}
	return inputLine + "\n\n" + indicator
}

func (m model) renderModeIndicator() string {
	label := m.styles.modeIndicator.Render(m.thinkingModeLabel())
	description := m.input.CompletionStyle.Inline(true).Render(" mode · Ctrl+T")
	return m.wrapParagraph(label+description, m.inputContentWidth())
}

func (m model) renderInputSegment(segment []rune, start, commandLength int) string {
	if len(segment) == 0 {
		return ""
	}

	end := start + len(segment)
	text := string(segment)
	renderPlain := m.input.TextStyle.Inline(true).Render
	if end <= commandLength {
		return m.styles.slashCommand.Render(text)
	}
	if start >= commandLength {
		return renderPlain(text)
	}

	split := commandLength - start
	return m.styles.slashCommand.Render(string(segment[:split])) + renderPlain(string(segment[split:]))
}

func (m model) contentWidth() int {
	if m.viewport.Width > 0 {
		return m.viewport.Width
	}
	horizontalFrame := m.styles.frame.GetHorizontalFrameSize() + 1
	if m.width > horizontalFrame {
		return m.width - horizontalFrame
	}
	return 20
}

func (m model) outerWidth() int {
	return m.contentWidth()
}

func (m model) inputContentWidth() int {
	width := m.outerWidth() - m.styles.inputBox.GetHorizontalFrameSize()
	if width < 1 {
		return 1
	}
	return width
}

func (m model) availableConversationHeight(totalHeight int) int {
	height := totalHeight - m.layoutReservedHeight()
	if height < 0 {
		return 0
	}
	return height
}

func (m model) layoutReservedHeight() int {
	reserved := m.styles.frame.GetVerticalFrameSize()

	if status := m.renderStatusLine(); status != "" {
		reserved += lipgloss.Height(status)
	}

	inputBody := m.fillLinesWithBackground(m.renderInputView(), m.inputContentWidth(), m.colors.bgRaised)
	input := m.styles.inputBox.Width(m.outerWidth()).Render(inputBody)
	reserved += lipgloss.Height(input)
	reserved += lipgloss.Height(m.renderFooterHelp())

	return reserved
}

func (m model) userBlockContentWidth() int {
	width := m.contentWidth() - m.styles.userBlock.GetHorizontalFrameSize()
	if width < 1 {
		return 1
	}
	return width
}

func (m model) renderHeader() string {
	return ""
}

func (m model) wrapParagraph(rendered string, width int) string {
	if width <= 0 || rendered == "" {
		return rendered
	}
	return wrap.String(rendered, width)
}

func (m model) fillLinesWithBackground(value string, width int, bg string) string {
	if value == "" || width <= 0 {
		return value
	}

	lines := strings.Split(value, "\n")
	padStyle := lipgloss.NewStyle().Background(lipgloss.Color(bg))
	for index, line := range lines {
		lineWidth := lipgloss.Width(line)
		if lineWidth >= width {
			continue
		}
		lines[index] = line + padStyle.Render(strings.Repeat(" ", width-lineWidth))
	}

	return strings.Join(lines, "\n")
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
