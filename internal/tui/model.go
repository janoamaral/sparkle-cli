package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/logico/sparkle-cli/internal/config"
	"github.com/logico/sparkle-cli/internal/ollama"
	"github.com/logico/sparkle-cli/internal/search"
	"github.com/logico/sparkle-cli/internal/slash"
)

const (
	idleThreshold          = 350 * time.Millisecond
	userBlockBackgroundHex = "#141414"
	thinkingToken          = "<|think|>"
	requestTimeoutFallback = 30 * time.Second
)

type colorScheme struct {
	name       string
	bgBase     string
	bgRaised   string
	border     string
	text       string
	textMuted  string
	textSubtle string
	status     string
	accent     string
	accentSoft string
	success    string
	error      string
}

func resolveColorScheme(name string) colorScheme {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "default":
		fallthrough
	default:
		return colorScheme{
			name:       "default",
			bgBase:     "#181818",
			bgRaised:   "#262626",
			border:     "#343434",
			text:       "#e7e7e7",
			textMuted:  "#9a9a9a",
			textSubtle: "#7e7e7e",
			status:     "#b3b3b3",
			accent:     "#81a1c1",
			accentSoft: "#88c0d0",
			success:    "#8fbcbb",
			error:      "#bf616a",
		}
	}
}

type state string

type interactionMode string

const (
	stateReady     state           = "ready"
	stateStreaming state           = "streaming"
	stateComplete  state           = "complete"
	modeNormal     interactionMode = "normal"
	modeReasoning  interactionMode = "reasoning"
	modeChat       interactionMode = "chat"
)

type messageBlock struct {
	role     string
	raw      string
	rendered string
	progress []search.ProgressUpdate
}

type streamEvent struct {
	chunk          string
	preparedPrompt string
	progress       *search.ProgressUpdate
	err            error
	done           bool
}

type streamChunkMsg struct{ content string }
type streamPreparedMsg struct{ prompt string }
type streamProgressMsg struct{ update search.ProgressUpdate }
type streamDoneMsg struct{}
type streamErrMsg struct{ err error }
type idleTickMsg time.Time

type requestStage string

const (
	requestStageSearch requestStage = "search"
	requestStageLLM    requestStage = "llm"
)

type searchPromptBuilder interface {
	Prepare(ctx context.Context, query string, searchQuery string, onActivity func(), onProgress func(search.ProgressUpdate)) (search.PreparedPrompt, error)
}

type model struct {
	cfg                config.Config
	client             *ollama.Client
	state              state
	input              textinput.Model
	viewport           viewport.Model
	spinner            spinner.Model
	blocks             []messageBlock
	session            []ollama.ChatMessage
	streamCh           <-chan streamEvent
	cancel             context.CancelFunc
	renderer           *glamour.TermRenderer
	lastTokenAt        time.Time
	spinnerVisible     bool
	activeBlockIndex   int
	progressBlockIndex int
	clipboardWrite     func(string) error
	openInEditor       func(string, string) tea.Cmd
	acceptedOutput     string
	exitCode           int
	width              int
	height             int
	status             string
	initialContext     string
	colors             colorScheme
	styles             styles
	searchBuilder      searchPromptBuilder
	requesting         bool
	userCanceled       bool
	llmTimerActive     bool
	llmTimerStartedAt  time.Time
	llmTimerPhase      string
	mode               interactionMode
	pendingUserInput   string
}

type llmAccumulator interface {
	StreamChatWithModel(ctx context.Context, model string, messages []ollama.ChatMessage, onChunk func(string) error) error
}

func noOpActivity() {
	_ = struct{}{}
}

func noTimeoutTriggered() bool { return false }

type promptPreparationContext struct {
	ctx              context.Context
	resolvedPrompt   string
	requestModel     string
	expansion        slash.Expansion
	searchTouch      func()
	searchTimedOut   func() bool
	startSearchTimer func()
	setLLMTimedOut   func(func() bool)
	llmTimedOut      func() bool
	stopSearchTimer  func()
	streamCh         chan<- streamEvent
}

type styles struct {
	frame           lipgloss.Style
	conversation    lipgloss.Style
	assistantBlock  lipgloss.Style
	thinkingBlock   lipgloss.Style
	inputBox        lipgloss.Style
	help            lipgloss.Style
	error           lipgloss.Style
	status          lipgloss.Style
	userBlock       lipgloss.Style
	userText        lipgloss.Style
	progressPending lipgloss.Style
	progressDone    lipgloss.Style
	progressInfo    lipgloss.Style
	keyBinding      lipgloss.Style
	slashCommand    lipgloss.Style
	separator       lipgloss.Style
	statusIndicator lipgloss.Style
	modeIndicator   lipgloss.Style
}

func Run(cfg config.Config, initialContext string) (string, int, error) {
	tuiModel := newModel(cfg, initialContext)
	program := tea.NewProgram(tuiModel, tea.WithAltScreen())
	finalModel, err := program.Run()
	if err != nil {
		return "", 3, err
	}

	result, ok := finalModel.(model)
	if !ok {
		return "", 3, fmt.Errorf("unexpected final model type %T", finalModel)
	}

	return result.acceptedOutput, result.exitCode, nil
}

func newModel(cfg config.Config, initialContext string) model {
	if normalizedEditor, err := config.NormalizeEditor(cfg.Editor); err == nil {
		cfg.Editor = normalizedEditor
	}

	colors := resolveColorScheme(cfg.Theme)

	input := textinput.New()
	input.Prompt = ""
	input.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.accent)).Background(lipgloss.Color(colors.bgRaised)).Bold(true)
	input.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.text)).Background(lipgloss.Color(colors.bgRaised))
	input.PlaceholderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.textMuted)).Background(lipgloss.Color(colors.bgRaised))
	input.CompletionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.textMuted)).Background(lipgloss.Color(colors.bgRaised))
	input.SetValue(initialContext)
	input.CursorEnd()
	input.Focus()
	input.CharLimit = 0
	input.ShowSuggestions = true
	input.SetSuggestions(slashCommandSuggestions(cfg.Commands))

	vp := viewport.New(0, 0)
	vp.Style = lipgloss.NewStyle().Background(lipgloss.Color(colors.bgBase))
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#3fa266")).Background(lipgloss.Color(colors.bgBase))

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(100),
	)

	sty := styles{
		frame:           lipgloss.NewStyle().Padding(1, 2, 0, 2).Background(lipgloss.Color(colors.bgBase)),
		conversation:    lipgloss.NewStyle().Background(lipgloss.Color(colors.bgBase)),
		assistantBlock:  lipgloss.NewStyle().Background(lipgloss.Color(colors.bgBase)),
		thinkingBlock:   lipgloss.NewStyle().Foreground(lipgloss.Color(colors.textSubtle)).Faint(true).Italic(true).Background(lipgloss.Color(colors.bgBase)),
		inputBox:        lipgloss.NewStyle().BorderStyle(lipgloss.ThickBorder()).BorderLeft(true).BorderTop(false).BorderRight(false).BorderBottom(false).BorderForeground(lipgloss.Color(colors.accent)).Padding(1, 2).Background(lipgloss.Color(colors.bgRaised)),
		help:            lipgloss.NewStyle().Foreground(lipgloss.Color(colors.textMuted)).Background(lipgloss.Color(colors.bgBase)),
		error:           lipgloss.NewStyle().Foreground(lipgloss.Color(colors.error)).Background(lipgloss.Color(colors.bgBase)),
		status:          lipgloss.NewStyle().Foreground(lipgloss.Color(colors.status)).Background(lipgloss.Color(colors.bgBase)),
		userBlock:       lipgloss.NewStyle().BorderStyle(lipgloss.ThickBorder()).BorderLeft(true).BorderTop(false).BorderRight(false).BorderBottom(false).BorderForeground(lipgloss.Color("#81a0c0")).Padding(1, 2).Background(lipgloss.Color(userBlockBackgroundHex)),
		userText:        lipgloss.NewStyle().Foreground(lipgloss.Color(colors.text)).Background(lipgloss.Color(userBlockBackgroundHex)),
		progressPending: lipgloss.NewStyle().Foreground(lipgloss.Color(colors.textSubtle)).Background(lipgloss.Color(colors.bgBase)).Faint(true),
		progressDone:    lipgloss.NewStyle().Foreground(lipgloss.Color(colors.success)).Background(lipgloss.Color(colors.bgBase)),
		progressInfo:    lipgloss.NewStyle().Foreground(lipgloss.Color(colors.status)).Background(lipgloss.Color(colors.bgBase)),
		keyBinding:      lipgloss.NewStyle().Foreground(lipgloss.Color(colors.accent)).Background(lipgloss.Color(colors.bgBase)),
		slashCommand:    lipgloss.NewStyle().Foreground(lipgloss.Color(colors.accentSoft)).Background(lipgloss.Color(colors.bgRaised)).Bold(true),
		separator:       lipgloss.NewStyle().Foreground(lipgloss.Color(colors.border)).Background(lipgloss.Color(colors.bgBase)),
		statusIndicator: lipgloss.NewStyle().Foreground(lipgloss.Color(colors.success)).Background(lipgloss.Color(colors.bgBase)),
		modeIndicator:   lipgloss.NewStyle().Foreground(lipgloss.Color(colors.accent)).Background(lipgloss.Color(colors.bgRaised)),
	}
	client := ollama.NewClient(cfg.OllamaURL, cfg.Model)

	model := model{
		cfg:                cfg,
		client:             client,
		state:              stateReady,
		input:              input,
		viewport:           vp,
		spinner:            sp,
		renderer:           renderer,
		activeBlockIndex:   -1,
		progressBlockIndex: -1,
		clipboardWrite:     writeClipboard,
		openInEditor:       editInExternalEditor,
		exitCode:           1,
		initialContext:     initialContext,
		colors:             colors,
		styles:             sty,
		searchBuilder:      search.NewService(cfg.SearchURL),
		status:             "Listo para recibir mensajes",
		mode:               modeNormal,
	}
	model.refreshViewport()
	return model
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m *model) appendBlock(role, content string) {
	block := messageBlock{role: role, raw: content}
	m.renderBlock(&block)
	m.blocks = append(m.blocks, block)
	m.refreshViewport()
}

func (m *model) appendProgressBlock() {
	block := messageBlock{role: "progress", progress: []search.ProgressUpdate{}}
	m.renderBlock(&block)
	m.blocks = append(m.blocks, block)
	m.progressBlockIndex = len(m.blocks) - 1
	m.refreshViewport()
}

func (m *model) updateBlock(index int, content string) {
	if index < 0 || index >= len(m.blocks) {
		return
	}
	m.blocks[index].raw = content
	m.renderBlock(&m.blocks[index])
	m.refreshViewport()
}

func (m *model) renderBlock(block *messageBlock) {
	header := m.renderBlockHeader(block.role)
	renderedContent := ""
	if block.role == "progress" {
		renderedContent = m.renderProgressContent(block.progress)
	} else {
		content := strings.TrimSpace(block.raw)
		renderedContent = m.renderBlockContent(block.role, content)
	}

	switch {
	case header == "":
		block.rendered = renderedContent
	case renderedContent == "":
		block.rendered = header
	default:
		block.rendered = header + "\n" + renderedContent
	}
}

func (m *model) renderBlockHeader(role string) string {
	return ""
}

func (m *model) renderBlockContent(role, content string) string {
	if content == "" {
		return ""
	}

	switch role {
	case "user":
		return m.renderUserBlockContent(content)
	case "error":
		return m.styles.error.Render(content)
	case "assistant":
		return m.renderAssistantContent(content)
	}

	return m.renderAssistantContent(content)
}

func (m *model) renderProgressContent(lines []search.ProgressUpdate) string {
	if len(lines) == 0 {
		return ""
	}

	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		if !shouldRenderProgressDiagnostic(line) {
			continue
		}
		text := strings.TrimSpace(line.Text)
		wrapped := m.wrapParagraph(text, m.contentWidth())
		rendered = append(rendered, m.styles.progressPending.Width(m.contentWidth()).Render(wrapped))
	}

	return strings.Join(rendered, "\n")
}

func shouldRenderProgressDiagnostic(line search.ProgressUpdate) bool {
	if strings.TrimSpace(line.Text) == "" {
		return false
	}

	switch line.Key {
	case "downloads":
		return line.State == search.ProgressDone
	case "token-estimate", "token-estimate-final":
		return true
	default:
		return false
	}
}

func progressIcon(kind search.ProgressKind) string {
	switch kind {
	case search.ProgressKindSearch:
		return "󰍉"
	case search.ProgressKindDownload:
		return ""
	case search.ProgressKindLLM:
		return "󰭻"
	default:
		return "•"
	}
}

func (m *model) updateProgress(update search.ProgressUpdate) {
	if m.progressBlockIndex < 0 || m.progressBlockIndex >= len(m.blocks) || m.blocks[m.progressBlockIndex].role != "progress" {
		m.appendProgressBlock()
	}

	block := &m.blocks[m.progressBlockIndex]
	for index := range block.progress {
		if block.progress[index].Key != update.Key {
			continue
		}
		block.progress[index] = update
		m.renderBlock(block)
		m.refreshViewport()
		return
	}

	block.progress = append(block.progress, update)
	m.renderBlock(block)
	m.refreshViewport()
}

func (m *model) startLLMTimer(phase string) {
	m.llmTimerActive = true
	m.llmTimerStartedAt = time.Now()
	m.llmTimerPhase = phase
	m.refreshLLMTimerDisplay()
}

func (m *model) setLLMTimerPhase(phase string) {
	if !m.llmTimerActive {
		return
	}
	m.llmTimerPhase = phase
	m.refreshLLMTimerDisplay()
}

func (m *model) stopLLMTimer() {
	if !m.llmTimerActive {
		return
	}
	elapsed := time.Since(m.llmTimerStartedAt).Round(time.Second)
	if elapsed < 0 {
		elapsed = 0
	}
	if m.progressBlockIndex >= 0 {
		m.updateProgress(search.ProgressUpdate{Key: "llm-elapsed", Kind: search.ProgressKindLLM, Text: fmt.Sprintf("Tiempo total del LLM: %ds", int(elapsed/time.Second)), State: search.ProgressDone})
	}
	m.llmTimerActive = false
	m.llmTimerStartedAt = time.Time{}
	m.llmTimerPhase = ""
}

func (m *model) refreshLLMTimerDisplay() {
	if !m.llmTimerActive {
		return
	}
	elapsed := time.Since(m.llmTimerStartedAt).Round(time.Second)
	if elapsed < 0 {
		elapsed = 0
	}
	seconds := int(elapsed / time.Second)
	phase := strings.TrimSpace(m.llmTimerPhase)
	if phase == "" {
		phase = "Consultando Ollama"
	}
	m.setStatus(fmt.Sprintf("%s... (%ds)", phase, seconds))
	if m.progressBlockIndex >= 0 {
		m.updateProgress(search.ProgressUpdate{Key: "llm-elapsed", Kind: search.ProgressKindLLM, Text: fmt.Sprintf("Tiempo transcurrido del LLM: %ds", seconds), State: search.ProgressInfo})
	}
}

func (m *model) renderAssistantContent(content string) string {
	thought, answer, active := splitThinkingOutput(content)
	sections := make([]string, 0, 2)

	if active && strings.TrimSpace(thought) != "" {
		sections = append(sections, m.renderThinkingContent(thought))
	}

	display := content
	if active {
		display = answer
	}
	if strings.TrimSpace(display) != "" {
		sections = append(sections, m.renderMarkdownContent(display))
	}

	return strings.Join(sections, "\n")
}

func (m *model) renderThinkingContent(content string) string {
	wrapped := m.wrapParagraph(strings.TrimSpace(content), m.contentWidth())
	return m.styles.thinkingBlock.Width(m.contentWidth()).Render(wrapped)
}

func (m *model) renderMarkdownContent(content string) string {
	if m.renderer == nil {
		wrapped := m.wrapParagraph(content, m.contentWidth())
		return m.styles.assistantBlock.Width(m.contentWidth()).Render(wrapped)
	}

	rendered, err := m.renderer.Render(content)
	if err != nil {
		wrapped := m.wrapParagraph(content, m.contentWidth())
		return m.styles.assistantBlock.Width(m.contentWidth()).Render(wrapped)
	}

	normalized := normalizeRenderedContent(rendered, 2)
	cleaned := stripANSIBackgroundCodes(normalized)
	prepared := m.renderAssistantWithBaseBackground(cleaned)
	return m.styles.assistantBlock.Width(m.contentWidth()).Render(prepared)
}

func (m model) renderAssistantWithBaseBackground(content string) string {
	if content == "" {
		return ""
	}

	background := ansiTrueColorBackgroundSequence(m.colors.bgBase)
	if background == "" {
		return content
	}

	prefix := "\x1b[0;" + background + "m"
	content = strings.ReplaceAll(content, "\x1b[m", prefix)
	content = strings.ReplaceAll(content, "\x1b[0m", prefix)
	content = strings.ReplaceAll(content, "\n", prefix+"\n"+prefix)

	if !strings.HasPrefix(content, prefix) {
		content = prefix + content
	}
	if !strings.HasSuffix(content, prefix) {
		content += prefix
	}

	return content
}

func ansiTrueColorBackgroundSequence(hex string) string {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(hex) != 6 {
		return ""
	}

	r, err := strconv.ParseInt(hex[0:2], 16, 0)
	if err != nil {
		return ""
	}
	g, err := strconv.ParseInt(hex[2:4], 16, 0)
	if err != nil {
		return ""
	}
	b, err := strconv.ParseInt(hex[4:6], 16, 0)
	if err != nil {
		return ""
	}

	return fmt.Sprintf("48;2;%d;%d;%d", r, g, b)
}

func (m *model) renderUserBlockContent(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	userSlashStyle := m.styles.slashCommand.Background(lipgloss.Color(userBlockBackgroundHex))
	command, remainder, ok := exactSlashCommand(trimmed, m.cfg.Commands)
	var rendered string
	if !ok {
		rendered = m.styles.userText.Render(trimmed)
	} else {
		remainder = strings.TrimLeftFunc(remainder, unicode.IsSpace)
		if remainder == "" {
			rendered = userSlashStyle.Render(command)
		} else {
			rendered = userSlashStyle.Render(command) + " " + m.styles.userText.Render(remainder)
		}
	}

	wrapped := m.wrapParagraph(rendered, m.userBlockContentWidth())
	return m.styles.userBlock.Width(m.contentWidth()).Render(wrapped)
}

func splitThinkingOutput(content string) (string, string, bool) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	trimmed := strings.TrimSpace(normalized)
	if trimmed == "" {
		return "", "", false
	}

	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "<think>") {
		remainder := trimmed[len("<think>"):]
		closingTag := "</think>"
		end := strings.Index(strings.ToLower(remainder), closingTag)
		if end < 0 {
			return strings.TrimSpace(remainder), "", true
		}
		return strings.TrimSpace(remainder[:end]), strings.TrimSpace(remainder[end+len(closingTag):]), true
	}

	if strings.HasPrefix(lower, "<|channel") || strings.HasPrefix(lower, "<channel") {
		index := strings.Index(lower, "thought\n")
		if index < 0 {
			return "", "", true
		}

		remainder := trimmed[index+len("thought\n"):]
		end, tagLen := findThinkingBoundary(strings.ToLower(remainder))
		if end < 0 {
			return strings.TrimSpace(remainder), "", true
		}
		return strings.TrimSpace(remainder[:end]), strings.TrimSpace(remainder[end+tagLen:]), true
	}

	return "", trimmed, false
}

func findThinkingBoundary(value string) (int, int) {
	tags := []string{"<channel|>", "<|channel|>", "<|/channel|>", "<|end|>"}
	bestIndex := -1
	bestLength := 0
	for _, tag := range tags {
		index := strings.Index(value, tag)
		if index < 0 {
			continue
		}
		if bestIndex == -1 || index < bestIndex {
			bestIndex = index
			bestLength = len(tag)
		}
	}
	return bestIndex, bestLength
}

func ensureThinkingToken(prompt string) string {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return thinkingToken
	}
	if strings.HasPrefix(trimmed, thinkingToken) {
		return trimmed
	}
	return thinkingToken + "\n" + trimmed
}

func stripThinkingToken(prompt string) string {
	trimmed := strings.TrimSpace(prompt)
	if strings.HasPrefix(trimmed, thinkingToken) {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, thinkingToken))
	}
	return trimmed
}

func (m model) requestSystemPrompt() string {
	if m.mode == modeReasoning {
		return ensureThinkingToken(m.cfg.SystemPrompt)
	}
	return stripThinkingToken(m.cfg.SystemPrompt)
}

func (m model) modeLabel() string {
	switch m.mode {
	case modeReasoning:
		return "Reasoning"
	case modeChat:
		return "Chat"
	default:
		return "Normal"
	}
}

func (m *model) cycleMode() {
	switch m.mode {
	case modeNormal:
		m.mode = modeReasoning
	case modeReasoning:
		m.mode = modeChat
	default:
		m.mode = modeNormal
	}
}

func (m model) buildRequestMessages(prompt string) []ollama.ChatMessage {
	requestMessages := make([]ollama.ChatMessage, 0, len(m.session)+2)
	requestMessages = append(requestMessages, ollama.ChatMessage{Role: "system", Content: m.requestSystemPrompt()})
	if m.mode == modeChat {
		requestMessages = append(requestMessages, m.session...)
	}
	requestMessages = append(requestMessages, ollama.ChatMessage{Role: "user", Content: prompt})
	return requestMessages
}

func slashCommandSuggestions(commands map[string]config.SlashCommand) []string {
	if len(commands) == 0 {
		return nil
	}

	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, "/"+name+" ")
	}
	sort.Strings(names)

	return names
}

func exactSlashCommand(input string, commands map[string]config.SlashCommand) (string, string, bool) {
	if input == "" || !strings.HasPrefix(input, "/") {
		return "", "", false
	}

	slashEnd := strings.IndexFunc(input, unicode.IsSpace)
	if slashEnd == -1 {
		slashEnd = len(input)
	}

	command := input[:slashEnd]
	if _, ok := commands[strings.TrimPrefix(command, "/")]; !ok {
		return "", "", false
	}

	return command, input[slashEnd:], true
}

func normalizeRenderedContent(rendered string, trimIndent int) string {
	trimmed := strings.Trim(rendered, "\n")
	if trimmed == "" {
		return ""
	}

	lines := strings.Split(trimmed, "\n")
	if trimIndent <= 0 {
		return trimmed
	}

	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[index] = ""
			continue
		}
		lines[index] = trimLeadingVisualIndent(line, trimIndent)
	}

	return strings.Join(lines, "\n")
}

func trimLeadingVisualIndent(line string, width int) string {
	prefixEnd := 0
	for prefixEnd < len(line) {
		if line[prefixEnd] != '\x1b' || prefixEnd+1 >= len(line) || line[prefixEnd+1] != '[' {
			break
		}

		sequenceEnd := prefixEnd + 2
		for sequenceEnd < len(line) {
			char := line[sequenceEnd]
			if char >= '@' && char <= '~' {
				sequenceEnd++
				break
			}
			sequenceEnd++
		}
		prefixEnd = sequenceEnd
	}

	prefix := line[:prefixEnd]
	rest := line[prefixEnd:]
	trimmed := 0
	for trimmed < len(rest) && width > 0 {
		switch rest[trimmed] {
		case ' ', '\t':
			trimmed++
			width--
		default:
			return prefix + rest[trimmed:]
		}
	}

	return prefix + rest[trimmed:]
}

func stripANSIBackgroundCodes(value string) string {
	if value == "" {
		return value
	}

	var out strings.Builder
	out.Grow(len(value))

	for index := 0; index < len(value); {
		if !startsCSI(value, index) {
			out.WriteByte(value[index])
			index++
			continue
		}

		seqStart := index
		paramsStart := index + 2
		cmdIndex, ok := findCSICommandEnd(value, paramsStart)
		if !ok {
			out.WriteString(value[seqStart:])
			break
		}

		command := value[cmdIndex]
		params := value[paramsStart:cmdIndex]
		index = cmdIndex + 1

		if command != 'm' {
			out.WriteString(value[seqStart:index])
			continue
		}

		filtered := filterBackgroundSGRParams(params)
		if filtered == "" {
			continue
		}
		out.WriteString("\x1b[")
		out.WriteString(filtered)
		out.WriteByte('m')
	}

	return out.String()
}

func stripANSISequences(value string) string {
	if value == "" {
		return value
	}

	var out strings.Builder
	out.Grow(len(value))

	for index := 0; index < len(value); {
		if !startsCSI(value, index) {
			out.WriteByte(value[index])
			index++
			continue
		}

		cmdIndex, ok := findCSICommandEnd(value, index+2)
		if !ok {
			break
		}
		index = cmdIndex + 1
	}

	return out.String()
}

func startsCSI(value string, index int) bool {
	return index+1 < len(value) && value[index] == '\x1b' && value[index+1] == '['
}

func findCSICommandEnd(value string, start int) (int, bool) {
	for index := start; index < len(value); index++ {
		char := value[index]
		if char >= '@' && char <= '~' {
			return index, true
		}
	}
	return -1, false
}

func filterBackgroundSGRParams(params string) string {
	if params == "" {
		return "0"
	}

	raw := strings.Split(params, ";")
	filtered := make([]string, 0, len(raw))

	for index := 0; index < len(raw); index++ {
		part := raw[index]
		if part == "" {
			continue
		}

		code, err := strconv.Atoi(part)
		if err != nil {
			filtered = append(filtered, part)
			continue
		}

		nextIndex, consumed := consumeExtendedBackground(raw, index, code)
		if consumed {
			index = nextIndex
			continue
		}

		if code == 0 {
			filtered = append(filtered, part)
			continue
		}

		if isBackgroundCode(code) {
			continue
		}

		filtered = append(filtered, part)
	}

	return strings.Join(filtered, ";")
}

func consumeExtendedBackground(raw []string, index, code int) (int, bool) {
	if code != 48 || index+1 >= len(raw) {
		return index, false
	}

	next := raw[index+1]
	if next == "5" && index+2 < len(raw) {
		return index + 2, true
	}
	if next == "2" && index+4 < len(raw) {
		return index + 4, true
	}

	return index, true
}

func isBackgroundCode(code int) bool {
	if code == 49 {
		return true
	}
	if code >= 40 && code <= 47 {
		return true
	}
	return code >= 100 && code <= 107
}

func waitForStream(ch <-chan streamEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok || event.done {
			return streamDoneMsg{}
		}
		if event.err != nil {
			return streamErrMsg{err: event.err}
		}
		if event.progress != nil {
			return streamProgressMsg{update: *event.progress}
		}
		if event.preparedPrompt != "" {
			return streamPreparedMsg{prompt: event.preparedPrompt}
		}
		return streamChunkMsg{content: event.chunk}
	}
}

func idleTick() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
		return idleTickMsg(t)
	})
}

func requestTimeoutDuration(seconds int) time.Duration {
	if seconds <= 0 {
		return requestTimeoutFallback
	}
	return time.Duration(seconds) * time.Second
}

type requestStageError struct {
	stage requestStage
	err   error
}

func (e requestStageError) Error() string {
	return e.err.Error()
}

func (e requestStageError) Unwrap() error {
	return e.err
}

func startIdleTimeoutWatcher(ctx context.Context, timeout time.Duration, cancel context.CancelFunc) (func(), func() bool, func()) {
	var timedOut atomic.Bool
	activityCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	var stopOnce sync.Once

	touch := func() {
		select {
		case activityCh <- struct{}{}:
		default:
		}
	}

	stop := func() {
		stopOnce.Do(func() {
			close(stopCh)
		})
	}

	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-activityCh:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(timeout)
			case <-timer.C:
				timedOut.Store(true)
				cancel()
				return
			}
		}
	}()

	touch()
	return touch, timedOut.Load, stop
}

func (m *model) startRequest(prompt string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.requesting = true
	m.userCanceled = false
	m.state = stateStreaming
	m.input.Blur()
	m.spinnerVisible = true
	m.lastTokenAt = time.Now().Add(-idleThreshold)

	expansion, err := slash.Resolve(prompt, m.cfg)
	if err != nil {
		return func() tea.Msg { return streamErrMsg{err: err} }
	}
	resolvedPrompt := expansion.Prompt
	requestModel := strings.TrimSpace(m.cfg.Model)
	if strings.TrimSpace(expansion.Model) != "" {
		requestModel = strings.TrimSpace(expansion.Model)
	}
	if expansion.Kind == slash.KindSearch {
		m.setStatus("Preparando busqueda web...")
	} else {
		m.startLLMTimer("Consultando Ollama")
	}

	m.appendBlock("user", prompt)
	m.progressBlockIndex = -1
	if expansion.Kind == slash.KindSearch {
		m.pendingUserInput = ""
	} else {
		m.pendingUserInput = resolvedPrompt
	}
	m.activeBlockIndex = -1

	streamCh := make(chan streamEvent)
	m.streamCh = streamCh

	go m.runRequestStream(ctx, cancel, resolvedPrompt, requestModel, expansion, streamCh)

	return tea.Batch(waitForStream(streamCh), idleTick())
}

func (m *model) runRequestStream(ctx context.Context, cancel context.CancelFunc, resolvedPrompt string, requestModel string, expansion slash.Expansion, streamCh chan<- streamEvent) {
	defer close(streamCh)

	llmTimedOut := func() bool { return false }
	searchTouch := noOpActivity
	searchTimedOut := noTimeoutTriggered
	stopSearchTimeout := noOpActivity
	startSearchTimeout := func() {
		stopSearchTimeout()
		searchTouch, searchTimedOut, stopSearchTimeout = startIdleTimeoutWatcher(ctx, requestTimeoutDuration(m.cfg.SearchTimeout), cancel)
	}
	defer stopSearchTimeout()

	promptForModel, err := m.preparePromptForModel(promptPreparationContext{
		ctx:            ctx,
		resolvedPrompt: resolvedPrompt,
		requestModel:   requestModel,
		expansion:      expansion,
		searchTouch: func() {
			searchTouch()
		},
		searchTimedOut: func() bool {
			return searchTimedOut()
		},
		startSearchTimer: startSearchTimeout,
		setLLMTimedOut: func(timedOut func() bool) {
			if timedOut != nil {
				llmTimedOut = timedOut
			}
		},
		llmTimedOut: func() bool {
			return llmTimedOut()
		},
		stopSearchTimer: stopSearchTimeout,
		streamCh:        streamCh,
	})
	if err != nil {
		return
	}
	stopSearchTimeout()

	requestMessages := m.buildRequestMessages(promptForModel)
	llmTimedOut, err = m.streamLLMWithAdaptiveTimeout(ctx, cancel, requestModel, requestMessages, func(chunk string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case streamCh <- streamEvent{chunk: chunk}:
			return nil
		}
	})
	if err != nil {
		streamCh <- streamEvent{err: stageRequestErr(requestStageLLM, normalizeRequestErr(err, llmTimedOut))}
		return
	}
	streamCh <- streamEvent{done: true}
}

func (m *model) preparePromptForModel(params promptPreparationContext) (string, error) {
	promptForModel := params.resolvedPrompt
	if params.expansion.Kind != slash.KindSearch {
		return promptForModel, nil
	}

	emitProgress := func(update search.ProgressUpdate) {
		select {
		case <-params.ctx.Done():
		case params.streamCh <- streamEvent{progress: &update}:
		}
	}

	searchQuery, rewriteTimedOut, err := m.rewriteSearchQuery(params.ctx, params.requestModel, params.resolvedPrompt)
	if rewriteTimedOut != nil {
		params.setLLMTimedOut(rewriteTimedOut)
	}
	if err != nil {
		params.streamCh <- streamEvent{err: stageRequestErr(requestStageLLM, normalizeRequestErr(err, params.llmTimedOut))}
		return "", err
	}
	params.startSearchTimer()
	prepared, err := m.searchBuilder.Prepare(params.ctx, params.resolvedPrompt, searchQuery, params.searchTouch, emitProgress)
	if err != nil {
		params.streamCh <- streamEvent{err: stageRequestErr(requestStageSearch, normalizeRequestErr(err, params.searchTimedOut))}
		return "", err
	}
	emitProgress(search.ProgressUpdate{Key: "token-estimate", Kind: search.ProgressKindStep, Text: fmt.Sprintf("Tokens: %d", prepared.ApproxTokens), State: search.ProgressInfo})
	promptForModel = prepared.Prompt
	if prepared.RequiresReduction(search.MaxPromptTokens) {
		emitProgress(search.ProgressUpdate{Key: "token-reduction", Kind: search.ProgressKindStep, Text: fmt.Sprintf("El contexto supera %d tokens. Resumiendo fuentes individualmente", search.MaxPromptTokens), State: search.ProgressPending})
		params.stopSearchTimer()
		reducedPrompt, reductionTimedOut, reduceErr := m.reduceSearchPrompt(params.ctx, params.requestModel, prepared, emitProgress)
		params.setLLMTimedOut(reductionTimedOut)
		if reduceErr != nil {
			params.streamCh <- streamEvent{err: stageRequestErr(requestStageLLM, normalizeRequestErr(reduceErr, params.llmTimedOut))}
			return "", reduceErr
		}
		promptForModel = reducedPrompt
		emitProgress(search.ProgressUpdate{Key: "token-reduction", Kind: search.ProgressKindStep, Text: "Resúmenes por fuente listos para el resumen final", State: search.ProgressDone})
	}

	params.searchTouch()
	select {
	case <-params.ctx.Done():
		stage := requestStageSearch
		timedOut := params.searchTimedOut
		if params.llmTimedOut() {
			stage = requestStageLLM
			timedOut = params.llmTimedOut
		}
		params.streamCh <- streamEvent{err: stageRequestErr(stage, normalizeRequestErr(params.ctx.Err(), timedOut))}
		return "", params.ctx.Err()
	case params.streamCh <- streamEvent{preparedPrompt: promptForModel}:
	}

	return promptForModel, nil
}

func (m *model) rewriteSearchQuery(ctx context.Context, requestModel string, originalQuery string) (string, func() bool, error) {
	messages := []ollama.ChatMessage{{Role: "system", Content: search.BuildSearchRewritePrompt(originalQuery)}}
	response, timedOut, err := m.collectLLMResponse(ctx, requestModel, messages)
	if err != nil {
		return "", timedOut, err
	}
	rewrittenQuery := search.ExtractPrimarySearchQuery(response)
	if strings.TrimSpace(rewrittenQuery) == "" {
		return strings.TrimSpace(originalQuery), timedOut, nil
	}
	return rewrittenQuery, timedOut, nil
}

func (m *model) reduceSearchPrompt(ctx context.Context, requestModel string, prepared search.PreparedPrompt, emitProgress func(search.ProgressUpdate)) (string, func() bool, error) {
	summaries := make([]search.SourceSummary, 0, len(prepared.Documents))
	llmTimedOut := func() bool { return false }
	for index, document := range prepared.Documents {
		progressKey := fmt.Sprintf("llm-source:%d", index+1)
		emitProgress(search.ProgressUpdate{
			Key:   progressKey,
			Kind:  search.ProgressKindLLM,
			Text:  fmt.Sprintf("Resumiendo fuente %d/%d: %s", index+1, len(prepared.Documents), document.URL),
			State: search.ProgressPending,
		})
		summary, timedOut, err := m.collectLLMResponse(ctx, requestModel, []ollama.ChatMessage{{Role: "user", Content: prepared.BuildDocumentPrompt(document)}})
		if timedOut != nil {
			llmTimedOut = timedOut
		}
		if err != nil {
			return "", llmTimedOut, err
		}
		summaries = append(summaries, search.SourceSummary{
			Title:   document.Title,
			URL:     document.URL,
			Summary: summary,
		})
		emitProgress(search.ProgressUpdate{
			Key:   progressKey,
			Kind:  search.ProgressKindLLM,
			Text:  fmt.Sprintf("Fuente %d/%d resumida: %s", index+1, len(prepared.Documents), document.URL),
			State: search.ProgressDone,
		})
	}

	finalPrompt := prepared.BuildFinalPrompt(summaries)
	emitProgress(search.ProgressUpdate{
		Key:   "token-estimate-final",
		Kind:  search.ProgressKindStep,
		Text:  fmt.Sprintf("Tokens tras reducción: %d", search.ApproximateTokenCount(finalPrompt)),
		State: search.ProgressInfo,
	})
	return finalPrompt, llmTimedOut, nil
}

func (m *model) collectLLMResponse(ctx context.Context, requestModel string, messages []ollama.ChatMessage) (string, func() bool, error) {
	var builder strings.Builder
	timedOut, err := m.streamLLMWithAdaptiveTimeout(ctx, nil, requestModel, messages, func(chunk string) error {
		builder.WriteString(chunk)
		return nil
	})
	if err != nil {
		return "", timedOut, err
	}
	return strings.TrimSpace(builder.String()), timedOut, nil
}

func (m *model) streamLLMWithAdaptiveTimeout(ctx context.Context, cancel context.CancelFunc, requestModel string, messages []ollama.ChatMessage, onChunk func(string) error) (func() bool, error) {
	if cancel == nil {
		var innerCancel context.CancelFunc
		ctx, innerCancel = context.WithCancel(ctx)
		defer innerCancel()
		cancel = innerCancel
	}

	currentTouch, currentTimedOut, stopCurrent := startIdleTimeoutWatcher(ctx, requestTimeoutDuration(m.cfg.LLMResolveTimeout), cancel)
	defer func() {
		if stopCurrent != nil {
			stopCurrent()
		}
	}()

	firstChunk := true
	err := m.client.StreamChatWithModel(ctx, requestModel, messages, func(chunk string) error {
		if firstChunk {
			if stopCurrent != nil {
				stopCurrent()
			}
			currentTouch, currentTimedOut, stopCurrent = startIdleTimeoutWatcher(ctx, requestTimeoutDuration(m.cfg.LLMTimeout), cancel)
			firstChunk = false
		}
		currentTouch()
		return onChunk(chunk)
	})

	return currentTimedOut, err
}

func normalizeRequestErr(err error, timedOut func() bool) error {
	if err == nil {
		return nil
	}
	if timedOut != nil && timedOut() && errors.Is(err, context.Canceled) {
		return context.DeadlineExceeded
	}
	return err
}

func stageRequestErr(stage requestStage, err error) error {
	if err == nil {
		return nil
	}
	var stageErr requestStageError
	if errors.As(err, &stageErr) {
		return err
	}
	return requestStageError{stage: stage, err: err}
}
