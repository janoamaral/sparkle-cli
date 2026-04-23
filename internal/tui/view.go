package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wrap"
)

var keyBindingTokens = []string{"Ctrl+Shift+N", "Ctrl+F", "Ctrl+N", "Ctrl+E", "Ctrl+L", "Ctrl+O", "Ctrl+Y", "Ctrl+T", "Ctrl+C", "Enter", "Tab", "Esc", "/", "󰘳+E", "󰘳+L", "󰘳+O", "󰘳+Y", "󰘳+T", "󰘳+C", "󰌑", "󰌒", "󱊷", ""}

func (m model) View() string {
	panes := m.renderContentPanes()
	inputBody := m.fillLinesWithBackground(m.renderInputView(), m.inputContentWidth(), m.colors.bgRaised)
	input := m.styles.inputBox.Width(m.outerWidth()).Render(inputBody)
	help := m.renderFooterHelp()

	sections := []string{panes}
	if m.state == stateSourceView && m.sourceSearchModalOpen {
		sections = append(sections, m.renderSourceSearchModal())
	}
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
	if m.status == "" || m.status == m.localizer.Get("status.ready") || m.status == m.localizer.Get("status.post_request") {
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
	if m.state == stateSourceSelect {
		return m.localizer.Get("help.source_select")
	}
	if m.state == stateSourceView || m.state == stateSourceLoading {
		return m.localizer.Get("help.source_view")
	}
	shortcuts := m.localizer.Get("help.shortcuts")
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
		return m.localizer.Get("help.no_slash_commands")
	}
	return fmt.Sprintf(m.localizer.Get("help.slash_commands_count"), len(m.cfg.Commands))
}

func (m *model) refreshViewport() {
	m.viewport.SetContent(m.mainViewportContent())
	if m.inSourceMode() {
		m.viewport.GotoTop()
		return
	}
	m.viewport.GotoBottom()
}

func (m *model) refreshSidebar() {
	m.sidebar.SetContent(m.sidebarContent())
	if len(m.sidebarTurns) > 0 {
		m.sidebar.GotoBottom()
		return
	}
	m.sidebar.GotoTop()
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
			body.WriteString("\n\n")
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
	renderer, err := newMarkdownRenderer(m.colors, wrap)
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
		suggestion := m.currentSuggestion()
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

func (m model) currentSuggestion() []rune {
	index := m.input.CurrentSuggestionIndex()
	matched := m.input.MatchedSuggestions()
	if index < 0 || index >= len(matched) {
		return nil
	}
	return []rune(matched[index])
}

func (m model) renderModeIndicator() string {
	label := m.styles.modeIndicator.Render(m.modeLabel())
	description := m.input.CompletionStyle.Inline(true).Render(m.localizer.Get("mode.indicator"))
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
	return m.mainPaneWidth()
}

func (m model) showSidebar() bool {
	return m.state == stateSourceView && m.sourceDocument != nil
}

func (m model) layoutContentWidth() int {
	horizontalFrame := m.styles.frame.GetHorizontalFrameSize() + 1
	if m.width > horizontalFrame {
		return m.width - horizontalFrame
	}
	return 20
}

func (m model) mainPaneWidth() int {
	total := m.layoutContentWidth()
	if !m.showSidebar() {
		return total
	}
	sidebar := m.sidebarWidth()
	main := total - sidebar - 1
	if main < 16 {
		main = max(16, total-sidebar)
	}
	if main < 1 {
		return 1
	}
	return main
}

func (m model) sidebarWidth() int {
	if !m.showSidebar() {
		return 0
	}
	total := m.layoutContentWidth()
	width := total / 3
	if width < 18 {
		width = 18
	}
	if width > 42 {
		width = 42
	}
	if total-width-1 < 16 {
		width = max(12, total-17)
	}
	if width >= total {
		width = max(1, total/2)
	}
	return width
}

func (m model) outerWidth() int {
	return m.layoutContentWidth()
}

func (m model) sidebarContentWidth() int {
	if !m.showSidebar() {
		return 0
	}
	width := m.sidebarWidth() - 2
	if width < 1 {
		return 1
	}
	return width
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

func (m *model) syncViewportLayout() {
	if m.height <= 0 {
		return
	}
	m.viewport.Height = m.availableConversationHeight(m.height)
	m.sidebar.Height = m.viewport.Height
}

func (m *model) setStatus(status string) {
	m.status = status
	m.syncViewportLayout()
}

func (m model) renderContentPanes() string {
	leftBody := m.fillLinesWithBackground(m.conversationViewportView(), m.mainPaneWidth(), m.colors.bgBase)
	left := m.styles.conversation.Width(m.mainPaneWidth()).Render(leftBody)
	if !m.showSidebar() {
		return left
	}
	rightBody := m.fillLinesWithBackground(m.sidebarViewportView(), m.sidebarContentWidth(), m.colors.bgRaised)
	right := lipgloss.NewStyle().Background(lipgloss.Color(m.colors.bgRaised)).BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).BorderTop(false).BorderRight(false).BorderBottom(false).BorderForeground(lipgloss.Color(m.colors.border)).PaddingLeft(1).Width(m.sidebarWidth()).Render(rightBody)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

func (m model) sidebarViewportView() string {
	if m.sidebar.Height <= 0 || m.sidebar.Width <= 0 {
		return ""
	}
	return m.sidebar.View()
}

func (m *model) mainViewportContent() string {
	if m.state == stateSourceSelect {
		return m.renderMarkdownContent(m.sourceSelectionMarkdown())
	}
	if m.state == stateSourceLoading {
		return m.renderMarkdownContent(m.sourceLoadingMarkdown())
	}
	if m.state == stateSourceView && m.sourceDocument != nil {
		return m.renderSourceDocumentContent()
	}
	return m.conversationContent()
}

func (m model) renderSourceSearchModal() string {
	title := m.styles.keyBinding.Copy().Background(lipgloss.Color(m.colors.bgRaised)).Bold(true).Render(m.localizer.Get("status.source_search_title"))
	input := m.sourceSearchInput.View()
	body := lipgloss.JoinVertical(lipgloss.Left, title, input)
	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.colors.accent)).
		Background(lipgloss.Color(m.colors.bgRaised)).
		Padding(0, 1).
		Width(max(24, m.mainPaneWidth()-4)).
		Render(body)
	return lipgloss.NewStyle().Background(lipgloss.Color(m.colors.bgBase)).Render(box)
}

func (m model) sidebarContent() string {
	width := m.sidebarContentWidth()
	if len(m.sidebarTurns) == 0 {
		return m.renderMarkdownContentWithWidth(m.sidebarPlaceholderMarkdown(), width, m.colors.bgRaised)
	}
	sections := make([]string, 0, len(m.sidebarTurns))
	for _, turn := range m.sidebarTurns {
		switch turn.role {
		case "user":
			sections = append(sections, m.renderUserBlockContentWithWidth(turn.content, width))
		default:
			sections = append(sections, m.renderAssistantContentWithWidth(turn.content, width, m.colors.bgRaised))
		}
	}
	return strings.Join(sections, "\n\n")
}

func (m model) sidebarPlaceholderMarkdown() string {
	if m.state == stateSourceView && m.sourceDocument != nil {
		return m.localizer.Get("pane.source_questions_title")
	}
	if len(m.lastSearchDocs) > 0 {
		return m.localizer.Get("pane.sidebar_title")
	}
	return m.localizer.Get("pane.no_sidebar_hint")
}

func (m model) sourceSelectionMarkdown() string {
	if len(m.lastSearchDocs) == 0 {
		return m.localizer.Get("pane.no_sources_available")
	}
	var body strings.Builder
	body.WriteString(m.localizer.Get("pane.source_selection"))
	limit := min(9, len(m.lastSearchDocs))
	for index := 0; index < limit; index++ {
		doc := m.lastSearchDocs[index]
		glyph := nerdFontCitationGlyphs[index+1]
		if glyph == "" {
			glyph = strconv.Itoa(index + 1)
		}
		title := strings.TrimSpace(doc.Title)
		if title == "" {
			title = strings.TrimSpace(doc.URL)
		}
		body.WriteString(fmt.Sprintf("- %s %d. %s\n  %s\n", glyph, index+1, title, strings.TrimSpace(doc.URL)))
	}
	return strings.TrimSpace(body.String())
}

func (m model) sourceLoadingMarkdown() string {
	if m.sourceSelectionIndex > 0 {
		return fmt.Sprintf(m.localizer.Get("pane.downloading_source"), m.sourceSelectionIndex)
	}
	return m.localizer.Get("pane.downloading_source_generic")
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
