package tui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
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
	"github.com/logico/sparkle-cli/internal/slash"
)

const idleThreshold = 350 * time.Millisecond

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
			bgBase:     "#141414",
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

const (
	stateReady     state = "ready"
	stateStreaming state = "streaming"
	stateComplete  state = "complete"
)

type messageBlock struct {
	role     string
	raw      string
	rendered string
}

type streamEvent struct {
	chunk string
	err   error
	done  bool
}

type streamChunkMsg struct{ content string }
type streamDoneMsg struct{}
type streamErrMsg struct{ err error }
type idleTickMsg time.Time

type model struct {
	cfg              config.Config
	client           *ollama.Client
	state            state
	input            textinput.Model
	viewport         viewport.Model
	spinner          spinner.Model
	blocks           []messageBlock
	session          []ollama.ChatMessage
	streamCh         <-chan streamEvent
	cancel           context.CancelFunc
	renderer         *glamour.TermRenderer
	lastTokenAt      time.Time
	spinnerVisible   bool
	activeBlockIndex int
	clipboardWrite   func(string) error
	acceptedOutput   string
	exitCode         int
	width            int
	height           int
	status           string
	initialContext   string
	colors           colorScheme
	styles           styles
	requesting       bool
}

type styles struct {
	frame           lipgloss.Style
	conversation    lipgloss.Style
	assistantBlock  lipgloss.Style
	inputBox        lipgloss.Style
	help            lipgloss.Style
	error           lipgloss.Style
	status          lipgloss.Style
	userBlock       lipgloss.Style
	userText        lipgloss.Style
	keyBinding      lipgloss.Style
	slashCommand    lipgloss.Style
	separator       lipgloss.Style
	statusIndicator lipgloss.Style
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
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.success)).Background(lipgloss.Color(colors.bgBase))

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(100),
	)

	sty := styles{
		frame:           lipgloss.NewStyle().Padding(1, 2).Background(lipgloss.Color(colors.bgBase)),
		conversation:    lipgloss.NewStyle().Background(lipgloss.Color(colors.bgBase)),
		assistantBlock:  lipgloss.NewStyle().Padding(1, 0).Background(lipgloss.Color("#141414")),
		inputBox:        lipgloss.NewStyle().BorderStyle(lipgloss.ThickBorder()).BorderLeft(true).BorderTop(false).BorderRight(false).BorderBottom(false).BorderForeground(lipgloss.Color(colors.accent)).Padding(1, 2).Background(lipgloss.Color(colors.bgRaised)),
		help:            lipgloss.NewStyle().Foreground(lipgloss.Color(colors.textMuted)).Background(lipgloss.Color(colors.bgBase)),
		error:           lipgloss.NewStyle().Foreground(lipgloss.Color(colors.error)).Background(lipgloss.Color(colors.bgBase)),
		status:          lipgloss.NewStyle().Foreground(lipgloss.Color(colors.status)).Background(lipgloss.Color(colors.bgBase)),
		userBlock:       lipgloss.NewStyle().BorderStyle(lipgloss.ThickBorder()).BorderLeft(true).BorderTop(false).BorderRight(false).BorderBottom(false).BorderForeground(lipgloss.Color("#81a0c0")).Padding(1, 2).Background(lipgloss.Color(colors.bgBase)),
		userText:        lipgloss.NewStyle().Foreground(lipgloss.Color(colors.text)).Background(lipgloss.Color(colors.bgBase)),
		keyBinding:      lipgloss.NewStyle().Foreground(lipgloss.Color(colors.accent)).Background(lipgloss.Color(colors.bgBase)),
		slashCommand:    lipgloss.NewStyle().Foreground(lipgloss.Color(colors.accentSoft)).Background(lipgloss.Color(colors.bgRaised)).Bold(true),
		separator:       lipgloss.NewStyle().Foreground(lipgloss.Color(colors.border)).Background(lipgloss.Color(colors.bgBase)),
		statusIndicator: lipgloss.NewStyle().Foreground(lipgloss.Color(colors.success)).Background(lipgloss.Color(colors.bgBase)),
	}

	model := model{
		cfg:              cfg,
		client:           ollama.NewClient(cfg.OllamaURL, cfg.Model),
		state:            stateReady,
		input:            input,
		viewport:         vp,
		spinner:          sp,
		renderer:         renderer,
		activeBlockIndex: -1,
		clipboardWrite:   writeClipboard,
		exitCode:         1,
		initialContext:   initialContext,
		colors:           colors,
		styles:           sty,
		status:           "Listo para recibir mensajes",
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
	content := strings.TrimSpace(block.raw)
	renderedContent := m.renderBlockContent(block.role, content)

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
	}

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
	return m.styles.assistantBlock.Width(m.contentWidth()).Render(cleaned)
}

func (m *model) renderUserBlockContent(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	userSlashStyle := m.styles.slashCommand.Background(lipgloss.Color(m.colors.bgBase))
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
		return ""
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
		return streamChunkMsg{content: event.chunk}
	}
}

func idleTick() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
		return idleTickMsg(t)
	})
}

func (m *model) startRequest(prompt string) tea.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Timeout)*time.Second)
	m.cancel = cancel
	m.requesting = true
	m.state = stateStreaming
	m.input.Blur()
	m.spinnerVisible = true
	m.lastTokenAt = time.Now().Add(-idleThreshold)
	m.status = "Consultando Ollama..."

	resolvedPrompt, _, err := slash.Expand(prompt, m.cfg)
	if err != nil {
		return func() tea.Msg { return streamErrMsg{err: err} }
	}

	m.appendBlock("user", prompt)
	m.session = append(m.session, ollama.ChatMessage{Role: "user", Content: prompt})
	m.activeBlockIndex = -1

	streamCh := make(chan streamEvent)
	m.streamCh = streamCh

	requestMessages := make([]ollama.ChatMessage, 0, len(m.session)+1)
	requestMessages = append(requestMessages, ollama.ChatMessage{Role: "system", Content: m.cfg.SystemPrompt})
	requestMessages = append(requestMessages, m.session[:len(m.session)-1]...)
	requestMessages = append(requestMessages, ollama.ChatMessage{Role: "user", Content: resolvedPrompt})

	go func() {
		defer close(streamCh)
		err := m.client.StreamChat(ctx, requestMessages, func(chunk string) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case streamCh <- streamEvent{chunk: chunk}:
				return nil
			}
		})
		if err != nil {
			streamCh <- streamEvent{err: err}
			return
		}
		streamCh <- streamEvent{done: true}
	}()

	return tea.Batch(waitForStream(streamCh), idleTick())
}
