package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/logico/sparkle-cli/internal/feedback"
	"github.com/muesli/reflow/wrap"
)

var keyBindingTokens = []string{"Ctrl+Shift+N", "Ctrl+F", "Ctrl+N", "Ctrl+Up", "Ctrl+Down", "Ctrl+E", "Ctrl+K", "Ctrl+L", "Ctrl+O", "Ctrl+Y", "Ctrl+T", "Ctrl+C", "Enter", "Tab", "Esc", "/", "󰘳+E", "󰘳+K", "󰘳+L", "󰘳+O", "󰘳+Y", "󰘳+T", "󰘳+C", "󰌑", "󰌒", "󱊷", ""}

func (m model) View() string {
	panes := m.renderContentPanes()
	inputBody := m.fillLinesWithBackground(m.renderInputView(), m.inputContentWidth(), m.colors.bgRaised)
	input := m.styles.inputBox.Width(m.outerWidth()).Render(inputBody)
	autocomplete := ""
	if !m.helpModalOpen {
		autocomplete = m.renderSlashAutocomplete()
	}

	sections := []string{panes}
	if m.state == stateSourceView && m.sourceSearchModalOpen {
		sections = append(sections, m.renderSourceSearchModal())
	}
	if status := m.renderStatusLine(); status != "" {
		sections = append(sections, m.renderInputTopSpacer(), status, input)
	} else {
		sections = append(sections, m.renderInputTopSpacer(), input)
	}
	body := lipgloss.JoinVertical(lipgloss.Left, sections...)
	view := m.styles.frame.Render(body)
	if autocomplete != "" {
		view = renderFloatingAutocomplete(view, body, input, autocomplete)
	}
	if m.helpModalOpen {
		view = renderCenteredOverlay(view, m.renderHelpModal())
	}

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

func renderFloatingAutocomplete(frameView string, body string, input string, popup string) string {
	if popup == "" || frameView == "" {
		return frameView
	}

	bodyHeight := lipgloss.Height(body)
	popupHeight := lipgloss.Height(popup)
	inputHeight := lipgloss.Height(input)
	if bodyHeight <= 0 || popupHeight <= 0 || inputHeight <= 0 {
		return frameView
	}

	inputStartInBody := bodyHeight - inputHeight
	if inputStartInBody < 0 {
		inputStartInBody = 0
	}
	popupTopInBody := inputStartInBody - popupHeight
	if popupTopInBody < 0 {
		popupTopInBody = 0
	}

	frameHeight := lipgloss.Height(frameView)
	frameWidth := lipgloss.Width(frameView)
	bodyWidth := lipgloss.Width(body)

	verticalInset := frameHeight - bodyHeight
	if verticalInset < 0 {
		verticalInset = 0
	}
	horizontalInset := 0
	if frameWidth > bodyWidth {
		horizontalInset = (frameWidth - bodyWidth) / 2
	}

	popupTop := verticalInset + popupTopInBody
	return overlayBlockAt(frameView, popup, popupTop, horizontalInset)
}

func renderCenteredOverlay(frameView string, overlay string) string {
	if overlay == "" || frameView == "" {
		return frameView
	}

	frameHeight := lipgloss.Height(frameView)
	frameWidth := lipgloss.Width(frameView)
	overlayHeight := lipgloss.Height(overlay)
	overlayWidth := lipgloss.Width(overlay)

	top := 0
	if frameHeight > overlayHeight {
		top = (frameHeight - overlayHeight) / 2
	}
	left := 0
	if frameWidth > overlayWidth {
		left = (frameWidth - overlayWidth) / 2
	}

	return overlayBlockAt(frameView, overlay, top, left)
}

func overlayBlockAt(base string, overlay string, top int, left int) string {
	if base == "" || overlay == "" {
		return base
	}

	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")

	for index, line := range overlayLines {
		target := top + index
		if target < 0 || target >= len(baseLines) {
			continue
		}

		overlaid := strings.Repeat(" ", max(0, left)) + line
		baseWidth := lipgloss.Width(baseLines[target])
		overlayWidth := lipgloss.Width(overlaid)
		if overlayWidth < baseWidth {
			overlaid += strings.Repeat(" ", baseWidth-overlayWidth)
		}
		baseLines[target] = overlaid
	}

	return strings.Join(baseLines, "\n")
}

func (m model) conversationViewportView() string {
	if m.viewport.Height <= 0 || m.viewport.Width <= 0 {
		return ""
	}
	return m.viewport.View()
}

func (m model) renderInputTopSpacer() string {
	return lipgloss.NewStyle().Background(lipgloss.Color(m.colors.bgBase)).Width(m.outerWidth()).Render(" ")
}

func (m model) renderStatusLine() string {
	showStatus := !(m.status == "" || m.status == m.localizer.Get("status.ready") || m.status == m.localizer.Get("status.post_request"))
	feedbackIndicator := m.renderFeedbackIndicator()
	if !showStatus && feedbackIndicator == "" {
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
	line := ""
	if showStatus {
		line = spinnerStyle.Render(prefix) + spaceStyle.Render(" ") + statusStyle.Render(status)
	}
	if feedbackIndicator != "" {
		if line != "" {
			line += spaceStyle.Render("  ")
		}
		line += feedbackIndicator
	}
	return lipgloss.NewStyle().Background(lipgloss.Color(m.colors.bgBase)).Width(m.outerWidth()).Render(line)
}

func (m model) renderFeedbackIndicator() string {
	if m.lastSearchInteractionID <= 0 {
		return ""
	}

	border := lipgloss.NewStyle().Foreground(lipgloss.Color(m.colors.textMuted)).Background(lipgloss.Color(m.colors.bgBase))
	symbol := "•"
	symbolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.colors.status)).Background(lipgloss.Color(m.colors.bgBase))

	switch m.feedbackRating {
	case feedback.VotePositive:
		symbol = ""
		symbolStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#38b46d")).Background(lipgloss.Color(m.colors.bgBase)).Bold(true)
	case feedback.VoteNegative:
		symbol = ""
		symbolStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#d75a5a")).Background(lipgloss.Color(m.colors.bgBase)).Bold(true)
	}

	return border.Render("[") + symbolStyle.Render(symbol) + border.Render("]")
}

func (m model) footerHelpText() string {
	return ""
}

func (m model) renderFooterHelp() string {
	if strings.TrimSpace(m.footerHelpText()) == "" {
		return ""
	}
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
	return ""
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
	inputLine = m.scrollInputLines(inputLine, position, 3)
	indicator := m.renderModeIndicator()
	if indicator == "" {
		return inputLine
	}
	if strings.TrimSpace(inputLine) == "" {
		return "\n\n" + indicator
	}
	return inputLine + "\n\n" + indicator
}

// scrollInputLines caps the rendered input to maxLines visible lines, scrolling
// to keep the cursor visible. position is the cursor's rune offset in the input value.
func (m model) scrollInputLines(inputLine string, position, maxLines int) string {
	lines := strings.Split(inputLine, "\n")
	if len(lines) <= maxLines {
		return inputLine
	}

	// Determine which visual line the cursor sits on by counting rune columns.
	promptWidth := lipgloss.Width(m.input.PromptStyle.Render(m.input.Prompt))
	contentWidth := m.inputContentWidth()
	cursorLine := 0
	if contentWidth > promptWidth && position > 0 {
		firstLineCapacity := contentWidth - promptWidth
		if position > firstLineCapacity && contentWidth > 0 {
			remaining := position - firstLineCapacity
			cursorLine = 1 + (remaining-1)/contentWidth
		}
	}
	if cursorLine >= len(lines) {
		cursorLine = len(lines) - 1
	}

	// Scroll window so the cursor line is always visible.
	start := cursorLine - maxLines + 1
	if start < 0 {
		start = 0
	}
	if start+maxLines > len(lines) {
		start = len(lines) - maxLines
	}

	return strings.Join(lines[start:start+maxLines], "\n")
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
	modeHint := m.input.CompletionStyle.Inline(true).Render(m.localizer.Get("mode.indicator"))
	helpHint := m.input.CompletionStyle.Inline(true).Render(m.localizer.Get("mode.help_indicator"))

	left := label + modeHint
	width := m.inputContentWidth()
	if width <= 0 {
		return left + "  " + helpHint
	}
	if lipgloss.Width(left)+2+lipgloss.Width(helpHint) > width {
		return m.wrapParagraph(left+"  "+helpHint, width)
	}

	gap := width - lipgloss.Width(left) - lipgloss.Width(helpHint)
	if gap < 2 {
		gap = 2
	}

	spacer := lipgloss.NewStyle().Background(lipgloss.Color(m.colors.bgRaised)).Render(strings.Repeat(" ", gap))
	return left + spacer + helpHint
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
	inputWidth := max(20, m.mainPaneWidth()-10)

	// Title with full background width
	titleText := m.localizer.Get("status.source_search_title")
	titleStyle := m.styles.keyBinding.
		Background(lipgloss.Color(m.colors.bgRaised)).
		Bold(true).
		Width(inputWidth).
		Padding(0, 0)
	title := titleStyle.Render(titleText)

	// Input with full background width
	input := m.sourceSearchInput.View()
	inputStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(m.colors.bgRaised)).
		Width(inputWidth).
		Padding(0, 0)
	inputLine := inputStyle.Render(input)

	body := lipgloss.JoinVertical(lipgloss.Left, title, inputLine)
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
		_, _ = fmt.Fprintf(&body, "- %s %d. %s\n  %s\n", glyph, index+1, title, strings.TrimSpace(doc.URL))
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

	reserved += lipgloss.Height(m.renderInputTopSpacer())

	inputBody := m.fillLinesWithBackground(m.renderInputView(), m.inputContentWidth(), m.colors.bgRaised)
	input := m.styles.inputBox.Width(m.outerWidth()).Render(inputBody)
	reserved += lipgloss.Height(input)
	reserved += lipgloss.Height(m.renderFooterHelp())

	return reserved
}

func (m model) wrapParagraph(rendered string, width int) string {
	if width <= 0 || rendered == "" {
		return rendered
	}
	return wrap.String(rendered, width)
}

// truncateToLines limits text to maxLines visual lines. If trimmed, the last
// visible line is suffixed with "…".
func truncateToLines(text string, maxLines int) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= maxLines {
		return text
	}
	last := lines[maxLines-1]
	// Trim trailing spaces before appending ellipsis.
	last = strings.TrimRight(last, " ") + "…"
	return strings.Join(append(lines[:maxLines-1], last), "\n")
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

func (m model) renderSlashAutocomplete() string {
	if !m.slashAutocompleteOpen || len(m.filteredSlashCommands) == 0 {
		return ""
	}

	type slashRow struct {
		command string
		desc    string
	}

	rows := make([]slashRow, 0, len(m.filteredSlashCommands))
	maxRowWidth := 0
	for _, cmdName := range m.filteredSlashCommands {
		row := slashRow{command: cmdName, desc: strings.TrimSpace(m.slashCommandDescription(cmdName))}
		rows = append(rows, row)

		plain := fmt.Sprintf("/%-14s", row.command)
		if row.desc != "" {
			plain += " " + row.desc + " "
		}
		if width := lipgloss.Width(plain); width > maxRowWidth {
			maxRowWidth = width
		}
	}

	if len(rows) == 0 {
		return ""
	}

	// Build the autocomplete items
	var items []string
	for i, row := range rows {
		bg := m.colors.bgBase
		if i == m.slashAutocompleteIndex {
			bg = m.colors.bgRaised
		}

		commandStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(m.colors.text)).
			Background(lipgloss.Color(bg)).
			Bold(true)
		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(m.colors.textMuted)).
			Background(lipgloss.Color(bg)).
			Faint(true)
		separatorStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(m.colors.textMuted)).
			Background(lipgloss.Color(bg))
		padStyle := lipgloss.NewStyle().Background(lipgloss.Color(bg))

		commandLabel := fmt.Sprintf("/%-14s", row.command)
		rendered := commandStyle.Render(commandLabel)
		plain := commandLabel
		if row.desc != "" {
			rendered += separatorStyle.Render(" ") + descStyle.Render(row.desc+" ")
			plain += " " + row.desc + " "
		}
		if fill := maxRowWidth - lipgloss.Width(plain); fill > 0 {
			rendered += padStyle.Render(strings.Repeat(" ", fill))
		}
		items = append(items, rendered)
	}

	// Join items and apply border
	content := lipgloss.JoinVertical(lipgloss.Left, items...)

	// Apply box styling
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.colors.accent)).
		Padding(0, 1).
		Background(lipgloss.Color(m.colors.bgBase))

	return boxStyle.Render(content)
}

func (m model) slashCommandDescription(command string) string {
	if strings.EqualFold(strings.TrimSpace(command), strings.TrimPrefix(slashCommandHelp, "/")) {
		return m.localizer.Get("help.slash.help")
	}
	cmd, ok := m.cfg.Commands[command]
	if !ok {
		return ""
	}
	return cmd.Desc
}

func (m model) helpModalMaxVisibleLines() int {
	return 14
}

func (m model) helpModalScrollLimit() int {
	lines := m.helpModalContentLines()
	visible := m.helpModalMaxVisibleLines()
	if len(lines) <= visible {
		return 0
	}
	return len(lines) - visible
}

func (m model) helpModalContentLines() []string {
	const modalBackground = "#181818"
	titleText := m.localizer.Get("help.modal.title")
	escText := m.localizer.Get("help.modal.esc")

	type helpRow struct {
		key  string
		desc string
	}

	shortcutRows := []helpRow{
		{key: "Enter", desc: m.localizer.Get("help.shortcut.enter")},
		{key: "Tab", desc: m.localizer.Get("help.shortcut.tab")},
		{key: "Ctrl+P", desc: m.localizer.Get("help.shortcut.ctrl_p")},
		{key: "Ctrl+S", desc: m.localizer.Get("help.shortcut.ctrl_s")},
		{key: "Ctrl+F", desc: m.localizer.Get("help.shortcut.ctrl_f")},
		{key: "Ctrl+N", desc: m.localizer.Get("help.shortcut.ctrl_n")},
		{key: "Ctrl+Shift+N", desc: m.localizer.Get("help.shortcut.ctrl_shift_n")},
		{key: "Ctrl+T", desc: m.localizer.Get("help.shortcut.ctrl_t")},
		{key: "Ctrl+Up", desc: m.localizer.Get("help.shortcut.ctrl_up")},
		{key: "Ctrl+Down", desc: m.localizer.Get("help.shortcut.ctrl_down")},
		{key: "Ctrl+K", desc: m.localizer.Get("help.shortcut.ctrl_k")},
		{key: "Ctrl+E", desc: m.localizer.Get("help.shortcut.ctrl_e")},
		{key: "Ctrl+L", desc: m.localizer.Get("help.shortcut.ctrl_l")},
		{key: "Ctrl+O", desc: m.localizer.Get("help.shortcut.ctrl_o")},
		{key: "Ctrl+Y", desc: m.localizer.Get("help.shortcut.ctrl_y")},
		{key: "Ctrl+C", desc: m.localizer.Get("help.shortcut.ctrl_c")},
		{key: "Esc", desc: m.localizer.Get("help.shortcut.esc")},
		{key: "Up/Down", desc: m.localizer.Get("help.shortcut.up_down")},
	}

	slashRows := make([]helpRow, 0, len(m.cfg.Commands)+1)
	slashRows = append(slashRows, helpRow{key: slashCommandHelp, desc: m.localizer.Get("help.slash.help")})

	names := make([]string, 0, len(m.cfg.Commands))
	for name := range m.cfg.Commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		command := m.cfg.Commands[name]
		slashRows = append(slashRows, helpRow{key: "/" + name, desc: strings.TrimSpace(command.Desc)})
	}

	maxLeftWidth := 0
	for _, row := range shortcutRows {
		if width := lipgloss.Width(row.key); width > maxLeftWidth {
			maxLeftWidth = width
		}
	}
	for _, row := range slashRows {
		if width := lipgloss.Width(row.key); width > maxLeftWidth {
			maxLeftWidth = width
		}
	}

	maxDescWidth := 0
	for _, row := range shortcutRows {
		if width := lipgloss.Width(row.desc); width > maxDescWidth {
			maxDescWidth = width
		}
	}
	for _, row := range slashRows {
		desc := strings.TrimSpace(row.desc)
		if desc == "" {
			desc = m.localizer.Get("help.slash.no_description")
		}
		if width := lipgloss.Width(desc); width > maxDescWidth {
			maxDescWidth = width
		}
	}

	totalRowWidth := maxLeftWidth + 2 + maxDescWidth
	if lipgloss.Width(titleText)+2+lipgloss.Width(escText) > totalRowWidth {
		totalRowWidth = lipgloss.Width(titleText) + 2 + lipgloss.Width(escText)
	}

	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.colors.text)).Background(lipgloss.Color(modalBackground)).Bold(false)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.colors.textMuted)).Background(lipgloss.Color(modalBackground))
	sectionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.colors.accent)).Background(lipgloss.Color(modalBackground)).Bold(true)
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.colors.accent)).Background(lipgloss.Color(modalBackground)).Bold(true)
	escStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.colors.textMuted)).Background(lipgloss.Color(modalBackground))
	gapStyle := lipgloss.NewStyle().Background(lipgloss.Color(modalBackground))

	headerGapWidth := totalRowWidth - lipgloss.Width(titleText) - lipgloss.Width(escText)
	if headerGapWidth < 2 {
		headerGapWidth = 2
	}
	header := titleStyle.Render(titleText) + gapStyle.Render(strings.Repeat(" ", headerGapWidth)) + escStyle.Render(escText)

	lines := []string{header, "", sectionStyle.Render(m.localizer.Get("help.modal.shortcuts"))}
	for _, row := range shortcutRows {
		left := fmt.Sprintf("%-*s", maxLeftWidth, row.key)
		right := fmt.Sprintf("%*s", maxDescWidth, row.desc)
		lines = append(lines, keyStyle.Render(left)+gapStyle.Render("  ")+descStyle.Render(right))
	}

	lines = append(lines, "", sectionStyle.Render(m.localizer.Get("help.modal.slash")))
	for _, row := range slashRows {
		left := fmt.Sprintf("%-*s", maxLeftWidth, row.key)
		desc := row.desc
		if strings.TrimSpace(desc) == "" {
			desc = m.localizer.Get("help.slash.no_description")
		}
		right := fmt.Sprintf("%*s", maxDescWidth, desc)
		lines = append(lines, keyStyle.Render(left)+gapStyle.Render("  ")+descStyle.Render(right))
	}

	return lines
}

func (m model) renderHelpModal() string {
	const modalBackground = "#181818"

	lines := m.helpModalContentLines()
	if len(lines) == 0 {
		return ""
	}

	maxVisible := m.helpModalMaxVisibleLines()
	if maxVisible <= 0 {
		maxVisible = 14
	}

	scroll := m.helpModalScroll
	if scroll < 0 {
		scroll = 0
	}
	limit := m.helpModalScrollLimit()
	if scroll > limit {
		scroll = limit
	}
	end := len(lines)
	if len(lines)-scroll > maxVisible {
		end = scroll + maxVisible
	}

	hasUp := scroll > 0
	hasDown := end < len(lines)
	hints := 0
	if hasUp {
		hints++
	}
	if hasDown {
		hints++
	}
	contentVisible := maxVisible - hints
	if contentVisible < 1 {
		contentVisible = 1
	}
	if len(lines)-scroll > contentVisible {
		end = scroll + contentVisible
		hasDown = end < len(lines)
		hints = 0
		if hasUp {
			hints++
		}
		if hasDown {
			hints++
		}
	}

	visible := lines[scroll:end]
	modalWidth := 0
	for _, line := range lines {
		if width := lipgloss.Width(line); width > modalWidth {
			modalWidth = width
		}
	}
	if modalWidth < 1 {
		modalWidth = 1
	}

	padBackground := lipgloss.NewStyle().Background(lipgloss.Color(modalBackground))
	padToWidth := func(line string) string {
		fill := modalWidth - lipgloss.Width(line)
		if fill <= 0 {
			return line
		}
		return line + padBackground.Render(strings.Repeat(" ", fill))
	}

	bodyLines := make([]string, 0, len(visible)+2)
	if hasUp {
		upHint := lipgloss.NewStyle().Foreground(lipgloss.Color(m.colors.textMuted)).Background(lipgloss.Color(modalBackground)).Render("↑")
		bodyLines = append(bodyLines, padToWidth(upHint))
	}
	for _, line := range visible {
		bodyLines = append(bodyLines, padToWidth(line))
	}
	if hasDown {
		downHint := lipgloss.NewStyle().Foreground(lipgloss.Color(m.colors.textMuted)).Background(lipgloss.Color(modalBackground)).Render("↓")
		bodyLines = append(bodyLines, padToWidth(downHint))
	}

	body := strings.Join(bodyLines, "\n")

	style := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.colors.accent)).
		Background(lipgloss.Color(modalBackground)).
		Padding(0, 1)

	return style.Render(body)
}
