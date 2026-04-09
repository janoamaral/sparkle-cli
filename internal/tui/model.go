package tui

import (
	"context"
	"fmt"
	"sort"
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
	acceptedOutput   string
	exitCode         int
	width            int
	height           int
	status           string
	initialContext   string
	styles           styles
	requesting       bool
}

type styles struct {
	frame        lipgloss.Style
	help         lipgloss.Style
	error        lipgloss.Style
	status       lipgloss.Style
	head         lipgloss.Style
	userHead     lipgloss.Style
	userText     lipgloss.Style
	keyBinding   lipgloss.Style
	slashCommand lipgloss.Style
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
	input := textinput.New()
	input.Prompt = " "
	input.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#CEFF00"))
	input.SetValue(initialContext)
	input.CursorEnd()
	input.Focus()
	input.CharLimit = 0
	input.ShowSuggestions = true
	input.SetSuggestions(slashCommandSuggestions(cfg.Commands))

	vp := viewport.New(0, 0)
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(100),
	)

	sty := styles{
		frame:        lipgloss.NewStyle().Padding(0, 1),
		help:         lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Faint(true),
		error:        lipgloss.NewStyle().Foreground(lipgloss.Color("203")),
		status:       lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Faint(true),
		head:         lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220")),
		userHead:     lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Faint(true),
		userText:     lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Faint(true),
		keyBinding:   lipgloss.NewStyle().Foreground(lipgloss.Color("#9ddadc")),
		slashCommand: lipgloss.NewStyle().Foreground(lipgloss.Color("#c85dad")),
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
		exitCode:         1,
		initialContext:   initialContext,
		styles:           sty,
		status:           "󰌑 para consultar · 󰘳+O acepta la ultima respuesta como comando.",
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
	switch role {
	case "user":
		return m.styles.userHead.Render("")
	case "assistant":
		return m.styles.head.Foreground(lipgloss.Color("#3489ff")).Render("")
	case "error":
		return m.styles.error.Render("Error")
	default:
		return ""
	}
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
		return content
	}

	rendered, err := m.renderer.Render(content)
	if err != nil {
		return content
	}

	return normalizeRenderedContent(rendered, 2)
}

func (m *model) renderUserBlockContent(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	command, remainder, ok := exactSlashCommand(trimmed, m.cfg.Commands)
	if !ok {
		return m.styles.userText.Render(trimmed)
	}
	remainder = strings.TrimLeftFunc(remainder, unicode.IsSpace)
	if remainder == "" {
		return m.styles.slashCommand.Render(command)
	}

	return m.styles.slashCommand.Render(command) + " " + m.styles.userText.Render(remainder)
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
	m.appendBlock("assistant", "")
	m.activeBlockIndex = len(m.blocks) - 1

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
