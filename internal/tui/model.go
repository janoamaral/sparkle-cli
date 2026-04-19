package tui

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"regexp"
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
	glamouransi "github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
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
	readyStatus            = "Listo para recibir mensajes"
	postRequestStatus      = "Ctrl+E abre editor del input · Ctrl+L limpia mensajes · Ctrl+O inserta en buffer · Ctrl+Y copia al clipboard · Enter envia otra consulta."
	progressKeyRewrite     = "rewrite-query"
	progressKeySearch      = "search-request"
	progressKeyDownloads   = "downloads"
	progressKeyDownloadsBk = "downloads-backup"
	progressKeyDownloadURL = "download:"
	progressKeyChunking    = "chunk-selection"
	progressKeyTokenUsage  = "token-estimate"
	progressKeyTokenFinal  = "token-estimate-final"
	progressKeyReduction   = "token-reduction"
	progressKeyLLM         = "llm"
	progressKeyLLMSource   = "llm-source:"
	progressSubtaskBuild   = "build-context"
	progressSubtaskReply   = "process-response"
	progressStepRewrite    = "Optimizando query"
	progressStepContext    = "Preparando contexto"
	progressStepResponse   = "Procesando respuesta"
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

type tokenUsage struct {
	system  int
	content int
}

const (
	slashCommandExplain      = "/explain"
	slashCommandTranslate    = "/translate"
	slashCommandGenerateCode = "/generate-code"
	slashCommandSearch       = "/search"
	slashCommandCheat        = "/cheat"
	slashCommandFix          = "/fix"
)

type slashPillPalette struct {
	foreground string
	background string
}

var slashCommandPalettes = []slashPillPalette{
	{foreground: "#ffb86c", background: "#3a2414"},
	{foreground: "#78c4ff", background: "#13283f"},
	{foreground: "#966ff8", background: "#211c33"},
	{foreground: "#e6d55a", background: "#312b12"},
	{foreground: "#5fe08a", background: "#17311f"},
	{foreground: "#ff8a7a", background: "#3a1e1b"},
	{foreground: "#c7c7cf", background: "#2c2c31"},
	{foreground: "#7ee7c7", background: "#17342c"},
}

var numericCitationPattern = regexp.MustCompile(`\[(\d+(?:\s*,\s*\d+)*)\]`)

var nerdFontCitationGlyphs = map[int]string{
	1: "󰲠",
	2: "󰲢",
	3: "󰲤",
	4: "󰲦",
	5: "󰲨",
	6: "󰲪",
	7: "󰲬",
	8: "󰲮",
	9: "󰲰",
}

var slashCommandPaletteOverrides = map[string]slashPillPalette{
	slashCommandExplain: {foreground: "#966ff8", background: "#211c33"},
}

var slashCommandGlyphs = map[string]string{
	slashCommandExplain:      "󰔨",
	slashCommandTranslate:    "󰗊",
	slashCommandGenerateCode: "",
	slashCommandSearch:       "",
	slashCommandCheat:        "󱃕",
	slashCommandFix:          "󰁨",
}

func (usage tokenUsage) total() int {
	return usage.system + usage.content
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
	diag     *searchDiagnostics
}

type searchDiagnostics struct {
	startedAt  time.Time
	finishedAt time.Time
	tasks      []diagnosticTask
}

type diagnosticTask struct {
	key        string
	title      string
	startedAt  time.Time
	finishedAt time.Time
	archived   bool
	subtasks   []diagnosticSubtask
}

type diagnosticSubtask struct {
	key        string
	title      string
	state      diagnosticState
	startedAt  time.Time
	finishedAt time.Time
}

type diagnosticState string

const (
	diagnosticTodo    diagnosticState = "todo"
	diagnosticWorking diagnosticState = "working"
	diagnosticDone    diagnosticState = "done"
	searchTaskSources string          = "search-sources"
	searchTaskProcess string          = "process-sources"
	searchTaskAnswer  string          = "generate-response"
)

type streamEvent struct {
	chunk          string
	preparedPrompt string
	preparedDocs   []search.Document
	progress       *search.ProgressUpdate
	err            error
	done           bool
}

type streamChunkMsg struct{ content string }
type streamPreparedMsg struct {
	prompt string
	docs   []search.Document
}
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
	pendingSearchDocs  []search.Document
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
	if closer, ok := result.searchBuilder.(io.Closer); ok {
		if closeErr := closer.Close(); closeErr != nil {
			return "", 3, closeErr
		}
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

	renderer, _ := newMarkdownRenderer(colors, 100)

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
		searchBuilder: search.NewService(
			cfg.SearchURL,
			search.WithEmbedder(client, cfg.SearchEmbeddingModel),
			search.WithQdrantCache(search.QdrantConfig{
				Enabled:        cfg.QdrantEnabled,
				Host:           cfg.QdrantHost,
				Port:           cfg.QdrantPort,
				APIKey:         cfg.QdrantAPIKey,
				UseTLS:         cfg.QdrantUseTLS,
				Collection:     cfg.QdrantCollection,
				ScoreThreshold: cfg.QdrantScoreThreshold,
				TTLHours:       cfg.QdrantTTLHours,
				PoolSize:       cfg.QdrantPoolSize,
			}),
		),
		status: readyStatus,
		mode:   modeNormal,
	}
	model.refreshViewport()
	return model
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func newMarkdownRenderer(colors colorScheme, wrap int) (*glamour.TermRenderer, error) {
	if wrap < 20 {
		wrap = 20
	}

	return glamour.NewTermRenderer(
		glamour.WithStyles(markdownStyleConfig(colors)),
		glamour.WithWordWrap(wrap),
	)
}

func markdownStyleConfig(colors colorScheme) glamouransi.StyleConfig {
	style := glamourstyles.DarkStyleConfig
	style.Document.StylePrimitive.Color = stringPtr(colors.text)
	style.Heading.StylePrimitive.Color = stringPtr(colors.accentSoft)
	style.Heading.StylePrimitive.Bold = boolPtr(true)
	style.Heading.StylePrimitive.Upper = boolPtr(true)

	clearHeadingPrefix := func(block *glamouransi.StyleBlock, color string) {
		block.StylePrimitive.Prefix = ""
		block.StylePrimitive.Suffix = ""
		block.StylePrimitive.Color = stringPtr(color)
		block.StylePrimitive.Bold = boolPtr(true)
		block.StylePrimitive.Upper = boolPtr(true)
	}

	clearHeadingPrefix(&style.H1, colors.accentSoft)
	style.H1.StylePrimitive.BackgroundColor = nil
	style.H1.StylePrimitive.Underline = boolPtr(true)

	clearHeadingPrefix(&style.H2, colors.accent)
	clearHeadingPrefix(&style.H3, colors.accent)
	clearHeadingPrefix(&style.H4, colors.accent)
	clearHeadingPrefix(&style.H5, colors.accent)
	clearHeadingPrefix(&style.H6, colors.accent)

	return style
}

func stringPtr(value string) *string {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func (m *model) appendBlock(role, content string) {
	block := messageBlock{role: role, raw: content}
	m.renderBlock(&block)
	m.blocks = append(m.blocks, block)
	m.refreshViewport()
}

func (m *model) appendProgressBlock() {
	block := messageBlock{role: "progress", progress: []search.ProgressUpdate{}, diag: newSearchDiagnostics(time.Now())}
	m.renderBlock(&block)
	m.blocks = append(m.blocks, block)
	m.progressBlockIndex = len(m.blocks) - 1
	m.refreshViewport()
}

func (m *model) clearConversation() tea.Cmd {
	if m.requesting {
		return nil
	}

	m.blocks = nil
	m.session = nil
	m.streamCh = nil
	m.cancel = nil
	m.activeBlockIndex = -1
	m.progressBlockIndex = -1
	m.pendingUserInput = ""
	m.spinnerVisible = false
	m.userCanceled = false
	m.llmTimerActive = false
	m.llmTimerStartedAt = time.Time{}
	m.llmTimerPhase = ""
	m.state = stateReady
	m.refreshViewport()
	m.setStatus("Mensajes eliminados.")
	return nil
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
		renderedContent = m.renderProgressContent(block.progress, block.diag)
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

func newSearchDiagnostics(now time.Time) *searchDiagnostics {
	return &searchDiagnostics{
		startedAt: now,
		tasks: []diagnosticTask{
			{
				key:   searchTaskSources,
				title: "Buscando fuentes",
				subtasks: []diagnosticSubtask{
					{key: progressKeyRewrite, title: progressStepRewrite, state: diagnosticTodo},
					{key: progressKeySearch, title: "Buscando fuentes", state: diagnosticTodo},
					{key: "download-sources", title: "Descargando fuentes", state: diagnosticTodo},
				},
			},
			{
				key:   searchTaskProcess,
				title: "Procesando fuentes",
				subtasks: []diagnosticSubtask{
					{key: "rank-sources", title: "Procesando relevancia", state: diagnosticTodo},
					{key: progressSubtaskBuild, title: progressStepContext, state: diagnosticTodo},
				},
			},
			{
				key:   searchTaskAnswer,
				title: "Generando respuesta",
				subtasks: []diagnosticSubtask{
					{key: progressSubtaskReply, title: progressStepResponse, state: diagnosticTodo},
				},
			},
		},
	}
}

func (d *searchDiagnostics) freeze(now time.Time) {
	if d == nil {
		return
	}
	if d.startedAt.IsZero() {
		d.startedAt = now
	}
	if d.finishedAt.IsZero() {
		d.finishedAt = now
	}
	for index := range d.tasks {
		task := &d.tasks[index]
		if task.startedAt.IsZero() {
			continue
		}
		if task.finishedAt.IsZero() {
			task.finishedAt = now
		}
		for subIndex := range task.subtasks {
			subtask := &task.subtasks[subIndex]
			if subtask.state == diagnosticWorking && subtask.finishedAt.IsZero() {
				subtask.finishedAt = now
			}
		}
	}
}

func (d *searchDiagnostics) markContextReady(now time.Time) {
	d.applyState(searchTaskProcess, progressSubtaskBuild, search.ProgressDone, now)
}

func (d *searchDiagnostics) markResponseStarted(now time.Time) {
	d.applyState(searchTaskAnswer, progressSubtaskReply, search.ProgressDone, now)
	d.freeze(now)
	for index := range d.tasks {
		if !d.tasks[index].startedAt.IsZero() {
			d.tasks[index].archived = true
		}
	}
}

func (d *searchDiagnostics) apply(update search.ProgressUpdate, now time.Time) {
	if d == nil {
		return
	}

	switch {
	case update.Key == progressKeyRewrite:
		d.applyState(searchTaskSources, progressKeyRewrite, update.State, now)
	case update.Key == progressKeySearch:
		d.applyState(searchTaskSources, progressKeySearch, update.State, now)
	case update.Key == progressKeyDownloads || update.Key == progressKeyDownloadsBk || strings.HasPrefix(update.Key, progressKeyDownloadURL):
		state := update.State
		if update.Key != progressKeyDownloads || update.State == search.ProgressInfo {
			state = search.ProgressPending
		}
		d.applyState(searchTaskSources, "download-sources", state, now)
	case update.Key == progressKeyChunking:
		d.applyState(searchTaskProcess, "rank-sources", update.State, now)
	case update.Key == progressKeyTokenUsage || update.Key == progressKeyReduction || update.Key == progressKeyTokenFinal || strings.HasPrefix(update.Key, progressKeyLLMSource):
		d.applyState(searchTaskProcess, progressSubtaskBuild, search.ProgressPending, now)
	case update.Key == progressKeyLLM:
		d.applyState(searchTaskAnswer, progressSubtaskReply, update.State, now)
	}
}

func (d *searchDiagnostics) applyState(taskKey, subtaskKey string, state search.ProgressState, now time.Time) {
	task, subtask := d.lookup(taskKey, subtaskKey)
	if task == nil || subtask == nil {
		return
	}
	if task.startedAt.IsZero() {
		task.startedAt = now
	}
	task.archived = false
	d.activate(taskKey)
	if subtask.startedAt.IsZero() {
		subtask.startedAt = now
	}

	switch state {
	case search.ProgressDone:
		subtask.state = diagnosticDone
		subtask.finishedAt = now
	default:
		if subtask.state != diagnosticDone {
			subtask.state = diagnosticWorking
		}
	}

	if task.isDone() {
		task.finishedAt = now
	}
}

func (d *searchDiagnostics) activate(taskKey string) {
	activeIndex := -1
	for index := range d.tasks {
		if d.tasks[index].key == taskKey {
			activeIndex = index
			break
		}
	}
	if activeIndex < 0 {
		return
	}
	for index := range d.tasks {
		if index < activeIndex && d.tasks[index].isDone() {
			d.tasks[index].archived = true
		}
		if index == activeIndex {
			d.tasks[index].archived = false
		}
	}
}

func (d *searchDiagnostics) lookup(taskKey, subtaskKey string) (*diagnosticTask, *diagnosticSubtask) {
	for taskIndex := range d.tasks {
		task := &d.tasks[taskIndex]
		if task.key != taskKey {
			continue
		}
		for subtaskIndex := range task.subtasks {
			subtask := &task.subtasks[subtaskIndex]
			if subtask.key == subtaskKey {
				return task, subtask
			}
		}
		return task, nil
	}
	return nil, nil
}

func (t diagnosticTask) isDone() bool {
	if len(t.subtasks) == 0 {
		return !t.finishedAt.IsZero()
	}
	for _, subtask := range t.subtasks {
		if subtask.state != diagnosticDone {
			return false
		}
	}
	return true
}

func (t diagnosticTask) visible() bool {
	if !t.startedAt.IsZero() || t.archived {
		return true
	}
	for _, subtask := range t.subtasks {
		if subtask.state != diagnosticTodo {
			return true
		}
	}
	return false
}

func (d *searchDiagnostics) elapsed(now time.Time) time.Duration {
	if d == nil || d.startedAt.IsZero() {
		return 0
	}
	end := now
	if !d.finishedAt.IsZero() {
		end = d.finishedAt
	}
	if end.Before(d.startedAt) {
		return 0
	}
	return end.Sub(d.startedAt)
}

func (t diagnosticTask) elapsed(now time.Time) time.Duration {
	if t.startedAt.IsZero() {
		return 0
	}
	end := now
	if !t.finishedAt.IsZero() {
		end = t.finishedAt
	}
	if end.Before(t.startedAt) {
		return 0
	}
	return end.Sub(t.startedAt)
}

func formatElapsedSeconds(elapsed time.Duration) string {
	if elapsed <= 0 {
		return "0s"
	}
	return fmt.Sprintf("%ds", int(elapsed.Round(time.Second)/time.Second))
}

func (m *model) renderSearchDiagnostics(diag *searchDiagnostics, now time.Time) string {
	if diag == nil {
		return ""
	}

	width := m.contentWidth()
	activeGlyph := lipgloss.NewStyle().Foreground(lipgloss.Color("#3cad88")).Background(lipgloss.Color(m.colors.bgBase)).Bold(true)
	activeText := lipgloss.NewStyle().Foreground(lipgloss.Color(m.colors.text)).Background(lipgloss.Color(m.colors.bgBase))
	archivedStyle := m.styles.progressPending
	workingSubtask := lipgloss.NewStyle().Foreground(lipgloss.Color(m.colors.text)).Background(lipgloss.Color(m.colors.bgBase))

	lines := []string{m.styles.progressInfo.Width(width).Render(m.wrapParagraph("Tiempo total ("+formatElapsedSeconds(diag.elapsed(now))+")", width))}
	for _, task := range diag.tasks {
		if !task.visible() {
			continue
		}

		headerText := task.title + " (" + formatElapsedSeconds(task.elapsed(now)) + ")"
		switch {
		case task.archived:
			lines = append(lines, archivedStyle.Width(width).Render(m.wrapParagraph("⬢ "+headerText, width)))
		default:
			glyphWidth := lipgloss.Width("⬢ ")
			textWidth := max(1, width-glyphWidth)
			line := activeGlyph.Render("⬢ ") + activeText.Width(textWidth).Render(m.wrapParagraph(headerText, textWidth))
			lines = append(lines, line)
		}

		for _, subtask := range task.subtasks {
			icon := "⃞"
			style := m.styles.progressPending
			switch subtask.state {
			case diagnosticDone:
				icon = "⊠"
			case diagnosticWorking:
				icon = "⊡"
				style = workingSubtask
			}
			line := "  " + icon + " " + subtask.title
			if task.archived {
				style = archivedStyle
			}
			lines = append(lines, style.Width(width).Render(m.wrapParagraph(line, width)))
		}
	}

	return strings.Join(lines, "\n")
}

func (m *model) renderProgressContent(lines []search.ProgressUpdate, diag *searchDiagnostics) string {
	if diag != nil {
		return m.renderSearchDiagnostics(diag, time.Now())
	}

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
	if block.diag != nil {
		block.diag.apply(update, time.Now())
	}
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

func (m *model) currentProgressBlock() *messageBlock {
	if m.progressBlockIndex < 0 || m.progressBlockIndex >= len(m.blocks) {
		return nil
	}
	if m.blocks[m.progressBlockIndex].role != "progress" {
		return nil
	}
	return &m.blocks[m.progressBlockIndex]
}

func (m *model) markSearchContextReady() {
	block := m.currentProgressBlock()
	if block == nil || block.diag == nil {
		return
	}
	block.diag.markContextReady(time.Now())
	m.renderBlock(block)
	m.refreshViewport()
}

func (m *model) markSearchResponseStarted() {
	block := m.currentProgressBlock()
	if block == nil || block.diag == nil {
		return
	}
	block.diag.markResponseStarted(time.Now())
	m.renderBlock(block)
	m.refreshViewport()
}

func (m *model) freezeProgressDiagnostics() {
	block := m.currentProgressBlock()
	if block == nil || block.diag == nil {
		return
	}
	block.diag.freeze(time.Now())
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
	display = replaceCitationMarkersWithGlyphs(display)
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
	command, remainder, ok := exactSlashCommand(trimmed, m.cfg.Commands)
	var rendered string
	if !ok {
		rendered = m.styles.userText.Render(trimmed)
	} else {
		remainder = strings.TrimLeftFunc(remainder, unicode.IsSpace)
		if remainder == "" {
			rendered = m.renderSlashCommandPill(command, userBlockBackgroundHex)
		} else {
			rendered = m.renderSlashCommandPill(command, userBlockBackgroundHex) + " " + m.styles.userText.Render(remainder)
		}
	}

	wrapped := m.wrapParagraph(rendered, m.userBlockContentWidth())
	return m.styles.userBlock.Width(m.contentWidth()).Render(wrapped)
}

func (m model) renderSlashCommandPill(command, surroundingBackground string) string {
	palette := slashCommandPaletteFor(command)
	separator := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.background)).Background(lipgloss.Color(surroundingBackground))
	label := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.foreground)).Background(lipgloss.Color(palette.background)).Bold(true)
	return separator.Render("") + label.Render(" "+slashCommandLabel(command)+" ") + separator.Render("")
}

func slashCommandLabel(command string) string {
	normalized := strings.ToLower(strings.TrimSpace(command))
	if glyph, ok := slashCommandGlyphs[normalized]; ok {
		return glyph + " " + normalized
	}
	return normalized
}

func slashCommandPaletteFor(command string) slashPillPalette {
	normalized := strings.ToLower(strings.TrimSpace(command))
	if palette, ok := slashCommandPaletteOverrides[normalized]; ok {
		return palette
	}

	if len(slashCommandPalettes) == 0 {
		return slashPillPalette{foreground: "#181818", background: "#81a1c1"}
	}

	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(normalized))
	index := int(hasher.Sum32() % uint32(len(slashCommandPalettes)))
	return slashCommandPalettes[index]
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

func replaceCitationMarkersWithGlyphs(content string) string {
	if strings.TrimSpace(content) == "" {
		return content
	}

	return numericCitationPattern.ReplaceAllStringFunc(content, func(match string) string {
		groups := numericCitationPattern.FindStringSubmatch(match)
		if len(groups) != 2 {
			return match
		}

		parts := strings.Split(groups[1], ",")
		glyphs := make([]string, 0, len(parts))
		for _, part := range parts {
			index, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil {
				return match
			}
			glyph, ok := nerdFontCitationGlyphs[index]
			if !ok {
				return match
			}
			glyphs = append(glyphs, glyph)
		}

		return strings.Join(glyphs, " ")
	})
}

func hasCitationMarkers(content string) bool {
	return numericCitationPattern.MatchString(content)
}

func buildSyntheticSourcesList(documents []search.Document) string {
	indexes := make([]int, 0, len(documents))
	for index := range documents {
		indexes = append(indexes, index+1)
	}
	return buildSyntheticSourcesListForIndexes(documents, indexes)
}

func buildSyntheticSourcesListForIndexes(documents []search.Document, indexes []int) string {
	if len(documents) == 0 || len(indexes) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("Fuentes Consultadas\n")
	for _, index := range indexes {
		if index <= 0 || index > len(documents) {
			continue
		}
		document := documents[index-1]
		builder.WriteString(fmt.Sprintf("- [%d] %s\n", index, strings.TrimSpace(document.URL)))
	}
	return strings.TrimSpace(builder.String())
}

func appendSyntheticSourcesIfMissing(raw string, documents []search.Document) string {
	if strings.TrimSpace(raw) == "" || len(documents) == 0 {
		return raw
	}

	_, answer, active := splitThinkingOutput(raw)
	processed := answer
	if active {
		sanitized, valid := sanitizeCitationMarkers(answer, len(documents))
		if sanitized != answer {
			answer = sanitized
			processed = sanitized
		}
		if valid {
			return raw
		}
	} else {
		sanitized, valid := sanitizeCitationMarkers(raw, len(documents))
		if sanitized != raw {
			processed = sanitized
		}
		if valid {
			return raw
		}
	}

	indexes := extractCitationIndexes(processed, len(documents))
	sources := buildSyntheticSourcesListForIndexes(documents, indexes)
	if sources == "" {
		sources = buildSyntheticSourcesList(documents)
	}
	if sources == "" {
		return raw
	}
	if active {
		prefix := strings.TrimSuffix(raw, answer)
		return strings.TrimRight(prefix+strings.TrimSpace(processed), "\n") + "\n\n" + sources
	}
	return strings.TrimRight(processed, "\n") + "\n\n" + sources
}

func sanitizeCitationMarkers(content string, maxIndex int) (string, bool) {
	if strings.TrimSpace(content) == "" {
		return content, false
	}
	valid := true
	sanitized := numericCitationPattern.ReplaceAllStringFunc(content, func(match string) string {
		groups := numericCitationPattern.FindStringSubmatch(match)
		if len(groups) != 2 {
			valid = false
			return ""
		}
		parts := strings.Split(groups[1], ",")
		kept := make([]string, 0, len(parts))
		for _, part := range parts {
			index, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || index <= 0 || index > maxIndex {
				valid = false
				continue
			}
			kept = append(kept, strconv.Itoa(index))
		}
		if len(kept) == 0 {
			return ""
		}
		return "[" + strings.Join(kept, ", ") + "]"
	})
	return strings.TrimSpace(sanitized), valid && hasCitationMarkers(sanitized)
}

func extractCitationIndexes(content string, maxIndex int) []int {
	matches := numericCitationPattern.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(matches))
	indexes := make([]int, 0, len(matches))
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		for _, part := range strings.Split(match[1], ",") {
			index, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || index <= 0 || index > maxIndex {
				continue
			}
			if _, ok := seen[index]; ok {
				continue
			}
			seen[index] = struct{}{}
			indexes = append(indexes, index)
		}
	}
	sort.Ints(indexes)
	return indexes
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

func countTokenUsage(messages []ollama.ChatMessage) tokenUsage {
	usage := tokenUsage{}
	for _, message := range messages {
		tokens := search.ApproximateTokenCount(message.Content)
		if strings.EqualFold(strings.TrimSpace(message.Role), "system") {
			usage.system += tokens
			continue
		}
		usage.content += tokens
	}
	return usage
}

func formatCompactTokenCount(tokens int) string {
	if tokens < 1000 {
		return strconv.Itoa(tokens)
	}
	value := float64(tokens) / 1000
	formatted := fmt.Sprintf("%.1fk", value)
	formatted = strings.Replace(formatted, ".0k", "k", 1)
	return formatted
}

func formatTokenUsage(usage tokenUsage) string {
	return fmt.Sprintf(
		"Tokens %s [ System %s · Content %s ]",
		formatCompactTokenCount(usage.total()),
		formatCompactTokenCount(usage.system),
		formatCompactTokenCount(usage.content),
	)
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
			return streamPreparedMsg{prompt: event.preparedPrompt, docs: append([]search.Document(nil), event.preparedDocs...)}
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

	emitProgress(search.ProgressUpdate{Key: progressKeyRewrite, Kind: search.ProgressKindStep, Text: progressStepRewrite, State: search.ProgressPending})
	searchQuery, rewriteTimedOut, err := m.rewriteSearchQuery(params.ctx, params.requestModel, params.resolvedPrompt)
	if rewriteTimedOut != nil {
		params.setLLMTimedOut(rewriteTimedOut)
	}
	if err != nil {
		params.streamCh <- streamEvent{err: stageRequestErr(requestStageLLM, normalizeRequestErr(err, params.llmTimedOut))}
		return "", err
	}
	emitProgress(search.ProgressUpdate{Key: progressKeyRewrite, Kind: search.ProgressKindStep, Text: progressStepRewrite, State: search.ProgressDone})
	params.startSearchTimer()
	prepared, err := m.searchBuilder.Prepare(params.ctx, params.resolvedPrompt, searchQuery, params.searchTouch, emitProgress)
	if err != nil {
		params.streamCh <- streamEvent{err: stageRequestErr(requestStageSearch, normalizeRequestErr(err, params.searchTimedOut))}
		return "", err
	}
	requestTokenUsage := countTokenUsage(m.buildRequestMessages(prepared.Prompt))
	emitProgress(search.ProgressUpdate{Key: progressKeyTokenUsage, Kind: search.ProgressKindStep, Text: formatTokenUsage(requestTokenUsage), State: search.ProgressInfo})
	promptForModel = prepared.Prompt
	if requestTokenUsage.total() > search.MaxPromptTokens {
		emitProgress(search.ProgressUpdate{Key: progressKeyReduction, Kind: search.ProgressKindStep, Text: fmt.Sprintf("El contexto supera %d tokens. Resumiendo fuentes individualmente", search.MaxPromptTokens), State: search.ProgressPending})
		params.stopSearchTimer()
		reducedPrompt, reductionTimedOut, reduceErr := m.reduceSearchPrompt(params.ctx, params.requestModel, prepared, emitProgress)
		params.setLLMTimedOut(reductionTimedOut)
		if reduceErr != nil {
			params.streamCh <- streamEvent{err: stageRequestErr(requestStageLLM, normalizeRequestErr(reduceErr, params.llmTimedOut))}
			return "", reduceErr
		}
		promptForModel = reducedPrompt
		reducedTokenUsage := countTokenUsage(m.buildRequestMessages(reducedPrompt))
		emitProgress(search.ProgressUpdate{Key: progressKeyTokenFinal, Kind: search.ProgressKindStep, Text: formatTokenUsage(reducedTokenUsage), State: search.ProgressInfo})
		emitProgress(search.ProgressUpdate{Key: progressKeyReduction, Kind: search.ProgressKindStep, Text: "Resúmenes por fuente listos para el resumen final", State: search.ProgressDone})
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
	case params.streamCh <- streamEvent{preparedPrompt: promptForModel, preparedDocs: append([]search.Document(nil), prepared.Documents...)}:
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
		progressKey := fmt.Sprintf("%s%d", progressKeyLLMSource, index+1)
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
