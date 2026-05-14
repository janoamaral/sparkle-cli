package tui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/logico/sparkle-cli/internal/config"
	"github.com/logico/sparkle-cli/internal/i18n"
	"github.com/logico/sparkle-cli/internal/ollama"
	"github.com/logico/sparkle-cli/internal/search"
	"github.com/logico/sparkle-cli/internal/slash"
)

const fixTemplate = "fix {{.Input}}"
const ticketTemplate = "ticket {{.Input}}"
const assistantResponse = "respuesta final"
const wantNilCmdMessage = "handleKeyMsg() cmd = %v, want nil"
const followUpPrompt = "como estas"
const pendingUserPrompt = "explicame ls"
const jsonContentTypeHeader = "Content-Type"
const jsonContentTypeValue = "application/json"
const doneChunkPayload = "{\"done\":true}\n"
const finalPromptText = "prompt final"
const sudoPromptOriginalQuery = "como cambiar el prompt de sudo"
const ollamaInstallQuestion = "how to install ollama?"
const requestingStatus = "Consultando Ollama..."
const wantEmptyPendingUserInput = "pendingUserInput = %q, want empty"
const preparePromptErrorFormat = "preparePromptForModel() error = %v"
const preparePromptValueFormat = "preparePromptForModel() prompt = %q, want %s"
const firstSourcePrefix = "- [1] "
const explainCommand = slashCommandExplain
const testSourceURLA = "https://example.test/a"
const testSourceURLB = "https://example.test/b"
const sourcesFooterHeading = "Fuentes:"
const progressStepRewrite = "Optimizando query"
const progressStepResponse = "Procesando respuesta"

func TestMain(m *testing.M) {
	_ = os.Setenv("LC_ALL", "es_ES.UTF-8")
	_ = os.Setenv("LANG", "es_ES.UTF-8")
	os.Exit(m.Run())
}

type stubSearchBuilder struct {
	prepared       search.PreparedPrompt
	err            error
	query          string
	searchQuery    string
	invokeResolver bool
	persist        func(query string, documents []search.Document, onProgress func(search.ProgressUpdate)) <-chan struct{}
	fetchSource    func(ctx context.Context, sourceURL string, title string, onActivity func(), onProgress func(search.ProgressUpdate)) (search.SourceDocument, error)
}

func (s *stubSearchBuilder) Prepare(ctx context.Context, query string, searchQuery string, resolveSearchQuery search.SearchQueryResolver, _ func(), _ func(search.ProgressUpdate)) (search.PreparedPrompt, error) {
	if s.err != nil {
		return search.PreparedPrompt{}, s.err
	}
	s.query = query
	s.searchQuery = searchQuery
	if s.invokeResolver && resolveSearchQuery != nil {
		resolvedPlan, err := resolveSearchQuery(ctx, query)
		if err != nil {
			return search.PreparedPrompt{}, err
		}
		s.searchQuery = resolvedPlan.EffectiveQuery()
	}
	return s.prepared, nil
}

func (s *stubSearchBuilder) PersistSemanticCache(query string, documents []search.Document, onProgress func(search.ProgressUpdate)) <-chan struct{} {
	if s.persist != nil {
		return s.persist(query, documents, onProgress)
	}
	done := make(chan struct{})
	close(done)
	return done
}

func (s *stubSearchBuilder) FetchSource(ctx context.Context, sourceURL string, title string, onActivity func(), onProgress func(search.ProgressUpdate)) (search.SourceDocument, error) {
	if s.fetchSource != nil {
		return s.fetchSource(ctx, sourceURL, title, onActivity, onProgress)
	}
	return search.SourceDocument{
		Document: search.Document{Title: title, URL: sourceURL, Content: "contenido"},
		Markdown: "# " + title + "\n\n" + sourceURL + "\n\ncontenido",
	}, nil
}

func TestSlashCommandSuggestionsSorted(t *testing.T) {
	commands := map[string]config.SlashCommand{
		"fix":           {Template: fixTemplate},
		"explain":       {Template: "explain {{.Input}}"},
		"generate-code": {Template: "generate {{.Input}}"},
	}

	got := slashCommandSuggestions(commands)
	want := []string{"/explain ", "/fix ", "/generate-code ", "/help "}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("slashCommandSuggestions() = %v, want %v", got, want)
	}
}

func TestRunRequestStreamStopsSearchTimeoutBeforeFinalLLM(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(jsonContentTypeHeader, jsonContentTypeValue)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not support flushing")
		}
		time.Sleep(1500 * time.Millisecond)
		_, _ = w.Write([]byte("{\"message\":{\"content\":\"respuesta\"}}\n"))
		flusher.Flush()
		_, _ = w.Write([]byte(doneChunkPayload))
	}))
	defer server.Close()

	m := newModel(config.Config{
		OllamaURL:         server.URL,
		Model:             "gemma4",
		SearchTimeout:     1,
		LLMResolveTimeout: 3,
		LLMTimeout:        3,
	}, "")
	m.client = ollama.NewClient(server.URL, "gemma4")
	m.searchBuilder = &stubSearchBuilder{prepared: search.PreparedPrompt{Query: "consulta", Prompt: finalPromptText}}

	streamCh := make(chan streamEvent, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go m.runRequestStream(ctx, cancel, "consulta", "gemma4", slash.Expansion{Prompt: "consulta", Used: true, Kind: slash.KindSearch}, streamCh)

	var sawChunk bool
	deadline := time.After(4 * time.Second)
	for {
		select {
		case event, ok := <-streamCh:
			if !ok {
				if !sawChunk {
					t.Fatal("stream closed before receiving llm chunk")
				}
				return
			}
			if event.err != nil {
				t.Fatalf("unexpected stream error: %v", event.err)
			}
			if event.chunk != "" {
				sawChunk = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for llm stream")
		}
	}
}

func TestPreparePromptForModelRewritesSearchQueryOnlyWhenSearchBuilderNeedsWebSearch(t *testing.T) {
	modelCh := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode rewrite request: %v", err)
		}
		modelCh <- request.Model
		w.Header().Set(jsonContentTypeHeader, jsonContentTypeValue)
		_, _ = w.Write([]byte("{\"message\":{\"content\":\"Query Primaria: sudo prompt change linux\\nQuery de Larga Cola: sudo prompt change linux ubuntu\\nBusqueda Tecnica: \\\"sudo prompt\\\" AND linux\"}}\n"))
		_, _ = w.Write([]byte(doneChunkPayload))
	}))
	defer server.Close()

	builder := &stubSearchBuilder{prepared: search.PreparedPrompt{Query: sudoPromptOriginalQuery, Prompt: finalPromptText}, invokeResolver: true}
	m := newModel(config.Config{OllamaURL: server.URL, SearchQueryModel: "gemma3:270m", Model: "gemma4"}, "")
	m.client = ollama.NewClient(server.URL, "gemma4")
	m.searchBuilder = builder

	streamCh := make(chan streamEvent, 4)
	prompt, err := m.preparePromptForModel(promptPreparationContext{
		ctx:              context.Background(),
		resolvedPrompt:   sudoPromptOriginalQuery,
		requestModel:     "gemma4",
		expansion:        slash.Expansion{Prompt: sudoPromptOriginalQuery, Used: true, Kind: slash.KindSearch},
		searchTouch:      noOpActivity,
		searchTimedOut:   noTimeoutTriggered,
		startSearchTimer: noOpActivity,
		setLLMTimedOut:   func(func() bool) { _ = struct{}{} },
		llmTimedOut:      noTimeoutTriggered,
		stopSearchTimer:  noOpActivity,
		streamCh:         streamCh,
	})
	if err != nil {
		t.Fatalf(preparePromptErrorFormat, err)
	}
	if prompt != finalPromptText {
		t.Fatalf(preparePromptValueFormat, prompt, finalPromptText)
	}
	if builder.query != sudoPromptOriginalQuery {
		t.Fatalf("searchBuilder original query = %q, want original query", builder.query)
	}
	if builder.searchQuery != "sudo prompt change linux" {
		t.Fatalf("searchBuilder search query = %q, want rewritten primary query", builder.searchQuery)
	}
	select {
	case got := <-modelCh:
		if got != "gemma3:270m" {
			t.Fatalf("rewrite model = %q, want gemma3:270m", got)
		}
	default:
		t.Fatal("expected rewrite request to Ollama")
	}
}

func TestPreparePromptForModelSkipsRewriteWhenSearchBuilderDoesNotNeedWebSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected rewrite request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	builder := &stubSearchBuilder{prepared: search.PreparedPrompt{Query: sudoPromptOriginalQuery, Prompt: finalPromptText}}
	m := newModel(config.Config{OllamaURL: server.URL, Model: "gemma4"}, "")
	m.client = ollama.NewClient(server.URL, "gemma4")
	m.searchBuilder = builder

	streamCh := make(chan streamEvent, 4)
	prompt, err := m.preparePromptForModel(promptPreparationContext{
		ctx:              context.Background(),
		resolvedPrompt:   sudoPromptOriginalQuery,
		requestModel:     "gemma4",
		expansion:        slash.Expansion{Prompt: sudoPromptOriginalQuery, Used: true, Kind: slash.KindSearch},
		searchTouch:      noOpActivity,
		searchTimedOut:   noTimeoutTriggered,
		startSearchTimer: noOpActivity,
		setLLMTimedOut:   func(func() bool) { _ = struct{}{} },
		llmTimedOut:      noTimeoutTriggered,
		stopSearchTimer:  noOpActivity,
		streamCh:         streamCh,
	})
	if err != nil {
		t.Fatalf(preparePromptErrorFormat, err)
	}
	if prompt != finalPromptText {
		t.Fatalf(preparePromptValueFormat, prompt, finalPromptText)
	}
	if builder.searchQuery != "" {
		t.Fatalf("searchBuilder search query = %q, want unresolved query because web search was skipped", builder.searchQuery)
	}
	if builder.query != sudoPromptOriginalQuery {
		t.Fatalf("searchBuilder original query = %q, want original query", builder.query)
	}
}

func TestPreparePromptForModelUsesResolvedSearchPayloadQuestion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(jsonContentTypeHeader, jsonContentTypeValue)
		response := "{\"message\":{\"content\":\"Query Primaria: install ollama linux\\n" +
			"Query de Larga Cola: how to install ollama on linux\\n" +
			"Busqueda Tecnica: \\\"install ollama\\\" linux\"}}\n"
		_, _ = w.Write([]byte(response))
		_, _ = w.Write([]byte(doneChunkPayload))
	}))
	defer server.Close()

	expansion, err := slash.Resolve("/search "+ollamaInstallQuestion, config.Config{Commands: map[string]config.SlashCommand{
		"search": {Template: "{{.Input}}", Kind: slash.KindSearch},
	}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	builder := &stubSearchBuilder{prepared: search.PreparedPrompt{Query: ollamaInstallQuestion, Prompt: finalPromptText}, invokeResolver: true}
	m := newModel(config.Config{
		OllamaURL: server.URL,
		Model:     "gemma4",
		Commands: map[string]config.SlashCommand{
			"search": {Template: "{{.Input}}", Kind: slash.KindSearch},
		},
	}, "")
	m.client = ollama.NewClient(server.URL, "gemma4")
	m.searchBuilder = builder

	streamCh := make(chan streamEvent, 4)
	prompt, err := m.preparePromptForModel(promptPreparationContext{
		ctx:              context.Background(),
		resolvedPrompt:   expansion.Prompt,
		requestModel:     "gemma4",
		expansion:        expansion,
		searchTouch:      noOpActivity,
		searchTimedOut:   noTimeoutTriggered,
		startSearchTimer: noOpActivity,
		setLLMTimedOut:   func(func() bool) { _ = struct{}{} },
		llmTimedOut:      noTimeoutTriggered,
		stopSearchTimer:  noOpActivity,
		streamCh:         streamCh,
	})
	if err != nil {
		t.Fatalf(preparePromptErrorFormat, err)
	}
	if prompt != finalPromptText {
		t.Fatalf(preparePromptValueFormat, prompt, finalPromptText)
	}

	if builder.query != ollamaInstallQuestion {
		t.Fatalf("searchBuilder original query = %q, want clean slash payload", builder.query)
	}
	if strings.Contains(builder.query, "/search") {
		t.Fatalf("searchBuilder original query = %q, should not contain slash prefix", builder.query)
	}
	if expansion.Prompt != ollamaInstallQuestion {
		t.Fatalf("Resolve() prompt = %q, want clean slash payload", expansion.Prompt)
	}
	if builder.searchQuery != "install ollama linux" {
		t.Fatalf("searchBuilder search query = %q, want rewritten install query", builder.searchQuery)
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
	userSlash := m.renderSlashCommandPill("/fix", userBlockBackgroundHex)
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

func TestRenderSlashCommandPillUsesPowerlineSeparators(t *testing.T) {
	m := newModel(config.Config{}, "")
	rendered := m.renderSlashCommandPill(explainCommand, userBlockBackgroundHex)
	palette := slashCommandPaletteFor(explainCommand)
	separator := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.background)).Background(lipgloss.Color(userBlockBackgroundHex)).Render("")
	label := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.foreground)).Background(lipgloss.Color(palette.background)).Bold(true).Render(" " + slashCommandLabel(explainCommand) + " ")
	closing := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.background)).Background(lipgloss.Color(userBlockBackgroundHex)).Render("")

	if rendered != separator+label+closing {
		t.Fatalf("renderSlashCommandPill() = %q, want powerline pill", rendered)
	}
}

func TestSlashCommandPaletteVariesByCommand(t *testing.T) {
	fixPalette := slashCommandPaletteFor(slashCommandFix)
	explainPalette := slashCommandPaletteFor(explainCommand)

	if fixPalette == explainPalette {
		t.Fatalf("slashCommandPaletteFor() returned the same palette for distinct commands: %+v", fixPalette)
	}
}

func TestSlashCommandPaletteUsesExplainOverride(t *testing.T) {
	palette := slashCommandPaletteFor(explainCommand)

	if palette.background != "#211c33" {
		t.Fatalf("slashCommandPaletteFor(%q) background = %q, want %q", explainCommand, palette.background, "#211c33")
	}
	if palette.foreground != "#966ff8" {
		t.Fatalf("slashCommandPaletteFor(%q) foreground = %q, want %q", explainCommand, palette.foreground, "#966ff8")
	}
}

func TestSlashCommandLabelUsesConfiguredGlyph(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{command: explainCommand, want: "󰔨 /explain"},
		{command: slashCommandGenerateCode, want: " /generate-code"},
		{command: slashCommandSearch, want: " /search"},
		{command: slashCommandCheat, want: "󱃕 /cheat"},
		{command: slashCommandFix, want: "󰁨 /fix"},
	}

	for _, test := range tests {
		if got := slashCommandLabel(test.command); got != test.want {
			t.Fatalf("slashCommandLabel(%q) = %q, want %q", test.command, got, test.want)
		}
	}
}

func TestRenderProgressContentShowsHierarchicalSearchDiagnostics(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 80
	now := time.Date(2026, time.April, 19, 10, 0, 0, 0, time.UTC)
	diag := m.newSearchDiagnostics(now)
	diag.apply(search.ProgressUpdate{Key: search.CacheLookupKey(), State: search.ProgressPending}, now)
	diag.apply(search.ProgressUpdate{Key: search.CacheLookupKey(), State: search.ProgressInfo}, now.Add(1*time.Second))
	diag.apply(search.ProgressUpdate{Key: progressKeyRewrite, State: search.ProgressPending}, now)
	diag.apply(search.ProgressUpdate{Key: progressKeyRewrite, State: search.ProgressDone}, now.Add(1*time.Second))
	diag.apply(search.ProgressUpdate{Key: progressKeySearch, State: search.ProgressPending}, now.Add(1*time.Second))
	diag.apply(search.ProgressUpdate{Key: progressKeySearch, State: search.ProgressDone}, now.Add(2*time.Second))
	diag.apply(search.ProgressUpdate{Key: progressKeyDownloads, Text: "Descargando hasta 3 candidatos para seleccionar 5 fuentes [instalar ollama, como instalar ollama, ollama]", State: search.ProgressPending}, now.Add(2*time.Second))
	diag.apply(search.ProgressUpdate{Key: progressKeyDownloads, State: search.ProgressDone}, now.Add(3*time.Second))
	diag.apply(search.ProgressUpdate{Key: progressKeyChunking, State: search.ProgressPending}, now.Add(3*time.Second))
	diag.apply(search.ProgressUpdate{Key: progressKeyChunking, State: search.ProgressDone}, now.Add(5*time.Second))
	diag.apply(search.ProgressUpdate{Key: search.CachePersistKey(), State: search.ProgressPending}, now.Add(5*time.Second))
	diag.apply(search.ProgressUpdate{Key: search.CachePersistKey(), State: search.ProgressDone}, now.Add(6*time.Second))
	diag.apply(search.ProgressUpdate{Key: progressKeyTokenUsage, State: search.ProgressInfo}, now.Add(5*time.Second))
	diag.markContextReady(now.Add(6 * time.Second))
	diag.apply(search.ProgressUpdate{Key: progressKeyLLM, State: search.ProgressPending}, now.Add(6*time.Second))

	rendered := m.renderSearchDiagnostics(diag, now.Add(6*time.Second))

	if !strings.Contains(rendered, "Tiempo total (6s)") {
		t.Fatalf("renderProgressContent() = %q, want global timer", rendered)
	}
	if !strings.Contains(rendered, "⬢ Buscando fuentes (6s)") {
		t.Fatalf("renderProgressContent() = %q, want completed sources task", rendered)
	}
	if !strings.Contains(rendered, "  ⊡ Consultando cache semantica") {
		t.Fatalf("renderProgressContent() = %q, want active semantic cache subtask", rendered)
	}
	if !strings.Contains(rendered, "  ⊠ "+progressStepRewrite) {
		t.Fatalf("renderProgressContent() = %q, want completed rewrite subtask", rendered)
	}
	if !strings.Contains(rendered, "  ⊠ Descargando fuentes") {
		t.Fatalf("renderProgressContent() = %q, want completed download subtask", rendered)
	}
	if !strings.Contains(rendered, "    ⊠ instalar ollama") {
		t.Fatalf("renderProgressContent() = %q, want download query child: instalar ollama", rendered)
	}
	if !strings.Contains(rendered, "    ⊠ como instalar ollama") {
		t.Fatalf("renderProgressContent() = %q, want download query child: como instalar ollama", rendered)
	}
	if !strings.Contains(rendered, "    ⊠ ollama") {
		t.Fatalf("renderProgressContent() = %q, want download query child: ollama", rendered)
	}
	if !strings.Contains(rendered, "  ⊠ Guardando cache semantica") {
		t.Fatalf("renderProgressContent() = %q, want completed cache persist subtask", rendered)
	}
	if !strings.Contains(rendered, "⬢ "+progressStepResponse+" (0s)") {
		t.Fatalf("renderProgressContent() = %q, want response task timer", rendered)
	}
	if !strings.Contains(rendered, "  ⊡ "+progressStepResponse) {
		t.Fatalf("renderProgressContent() = %q, want active response subtask with single-space glyph separation", rendered)
	}
}

func TestRenderSearchDiagnosticsUsesNonCombiningGlyphForPendingSubtasks(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 80
	now := time.Date(2026, time.April, 19, 10, 0, 0, 0, time.UTC)
	diag := m.newSearchDiagnostics(now)
	diag.apply(search.ProgressUpdate{Key: progressKeyChunking, State: search.ProgressPending}, now)

	rendered := m.renderSearchDiagnostics(diag, now)

	if !strings.Contains(rendered, "  ☐ Guardando cache semantica") {
		t.Fatalf("renderSearchDiagnostics() = %q, want pending subtask glyph rendered with visible space", rendered)
	}
	if strings.Contains(rendered, "  ⃞ Guardando cache semantica") {
		t.Fatalf("renderSearchDiagnostics() = %q, want pending subtask to avoid combining checkbox glyph", rendered)
	}
}

func TestExtractDownloadDiagnosticDetail(t *testing.T) {
	detail := extractDownloadDiagnosticDetail("Descargando hasta 3 candidatos para seleccionar 5 fuentes [instalar ollama, como instalar ollama, ollama]")
	if detail != "instalar ollama, como instalar ollama, ollama" {
		t.Fatalf("extractDownloadDiagnosticDetail() = %q, want query list", detail)
	}

	if got := extractDownloadDiagnosticDetail("Descargando fuentes"); got != "" {
		t.Fatalf("extractDownloadDiagnosticDetail() = %q, want empty detail when no bracketed queries", got)
	}
}

func TestReplaceCitationMarkersWithGlyphs(t *testing.T) {
	input := "Respuesta con soporte [1] y conflicto [2, 3].\n\nFuentes:\n- [1] https://example.test/a\n- [2] https://example.test/b\n- [3] https://example.test/c"

	got := replaceCitationMarkersWithGlyphs(input)

	if strings.Contains(got, "[1]") || strings.Contains(got, "[2, 3]") {
		t.Fatalf("replaceCitationMarkersWithGlyphs() = %q, want numeric citations replaced", got)
	}
	if !strings.Contains(got, "󰲠") {
		t.Fatalf("replaceCitationMarkersWithGlyphs() = %q, want first citation glyph", got)
	}
	if !strings.Contains(got, "󰲢 󰲤") {
		t.Fatalf("replaceCitationMarkersWithGlyphs() = %q, want grouped citation glyphs", got)
	}
}

func TestAppendSyntheticSourcesIfMissingAppendsLinksWhenNoCitationsExist(t *testing.T) {
	m := newModel(config.Config{}, "")
	input := "Respuesta final sin citas"
	documents := []search.Document{{URL: testSourceURLA}, {URL: testSourceURLB}}

	got := m.appendSyntheticSourcesIfMissing(input, documents)

	if !strings.Contains(got, "Respuesta final sin citas") {
		t.Fatalf("appendSyntheticSourcesIfMissing() = %q, want original answer preserved", got)
	}
	if !strings.Contains(got, sourcesFooterHeading) {
		t.Fatalf("appendSyntheticSourcesIfMissing() = %q, want sources footer appended", got)
	}
	if !strings.Contains(got, firstSourcePrefix+testSourceURLA) || !strings.Contains(got, "- [2] "+testSourceURLB) {
		t.Fatalf("appendSyntheticSourcesIfMissing() = %q, want numbered source links", got)
	}
}

func TestAppendSyntheticSourcesIfMissingLeavesCitedAnswerUntouched(t *testing.T) {
	m := newModel(config.Config{}, "")
	input := "Respuesta final con cita [1]"
	documents := []search.Document{{URL: testSourceURLA}}

	got := m.appendSyntheticSourcesIfMissing(input, documents)

	if got != input {
		t.Fatalf("appendSyntheticSourcesIfMissing() = %q, want unchanged cited answer", got)
	}
}

func TestAppendSyntheticSourcesIfMissingStripsInvalidCitationsAndRebuildsSources(t *testing.T) {
	m := newModel(config.Config{}, "")
	input := "Respuesta final con cita invalida [3]"
	documents := []search.Document{{URL: testSourceURLA}}

	got := m.appendSyntheticSourcesIfMissing(input, documents)

	if strings.Contains(got, "[3]") {
		t.Fatalf("appendSyntheticSourcesIfMissing() = %q, want invalid citation removed", got)
	}
	if !strings.Contains(got, sourcesFooterHeading) {
		t.Fatalf("appendSyntheticSourcesIfMissing() = %q, want rebuilt sources footer", got)
	}
	if !strings.Contains(got, firstSourcePrefix+testSourceURLA) {
		t.Fatalf("appendSyntheticSourcesIfMissing() = %q, want valid synthetic source list", got)
	}
}

func TestHandleStreamProgressCreatesProgressBlock(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.progressBlockIndex = -1

	cmd := m.handleStreamProgress(streamProgressMsg{update: search.ProgressUpdate{Key: "search-request", Kind: search.ProgressKindSearch, Text: "https://search.example.test?q=sudo", State: search.ProgressPending}})

	if cmd == nil {
		t.Fatal("handleStreamProgress() cmd = nil, want wait command")
	}
	if m.progressBlockIndex < 0 {
		t.Fatal("expected progress block to be created")
	}
	if m.blocks[m.progressBlockIndex].role != "progress" {
		t.Fatalf("progress block role = %q, want progress", m.blocks[m.progressBlockIndex].role)
	}
	if len(m.blocks[m.progressBlockIndex].progress) != 1 {
		t.Fatalf("progress line count = %d, want 1", len(m.blocks[m.progressBlockIndex].progress))
	}
	if m.blocks[m.progressBlockIndex].diag == nil {
		t.Fatal("expected hierarchical diagnostics state to be initialized")
	}
}

func TestHandleStreamProgressUsesSpecificCacheAndDownloadStatuses(t *testing.T) {
	m := newModel(config.Config{}, "")

	m.handleStreamProgress(streamProgressMsg{update: search.ProgressUpdate{Key: search.CacheLookupKey(), State: search.ProgressInfo, Text: "Cache semantica sin hits; continuando con busqueda web"}})
	if m.status != "Cache semantica sin hits; buscando en la web..." {
		t.Fatalf("status = %q, want cache miss status", m.status)
	}

	m.handleStreamProgress(streamProgressMsg{update: search.ProgressUpdate{Key: progressKeyDownloads, State: search.ProgressPending}})
	if m.status != "Descargando fuentes..." {
		t.Fatalf("status = %q, want downloads status", m.status)
	}

	m.handleStreamProgress(streamProgressMsg{update: search.ProgressUpdate{Key: search.CachePersistKey(), State: search.ProgressDone}})
	if m.status != "Cache semantica actualizada en Qdrant." {
		t.Fatalf("status = %q, want cache persist status", m.status)
	}
}

func TestHandleStreamChunkArchivesSearchDiagnosticsOnFirstToken(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 80
	m.appendProgressBlock()
	block := &m.blocks[m.progressBlockIndex]
	now := time.Date(2026, time.April, 19, 10, 0, 0, 0, time.UTC)
	block.diag.startedAt = now
	block.diag.apply(search.ProgressUpdate{Key: progressKeyRewrite, State: search.ProgressPending}, now)
	block.diag.apply(search.ProgressUpdate{Key: progressKeyRewrite, State: search.ProgressDone}, now.Add(1*time.Second))
	block.diag.apply(search.ProgressUpdate{Key: progressKeySearch, State: search.ProgressPending}, now.Add(1*time.Second))
	block.diag.apply(search.ProgressUpdate{Key: progressKeySearch, State: search.ProgressDone}, now.Add(2*time.Second))
	block.diag.apply(search.ProgressUpdate{Key: progressKeyLLM, State: search.ProgressPending}, now.Add(2*time.Second))

	cmd := m.handleStreamChunk(streamChunkMsg{content: "hola"})

	if cmd == nil {
		t.Fatal("handleStreamChunk() cmd = nil, want wait command")
	}
	if m.activeBlockIndex < 0 {
		t.Fatal("expected assistant block to be created")
	}
	for _, task := range block.diag.tasks {
		if task.startedAt.IsZero() {
			continue
		}
		if !task.archived {
			t.Fatalf("task %q archived = false, want true after first token", task.key)
		}
	}
	rendered := m.renderSearchDiagnostics(block.diag, block.diag.finishedAt)
	if !strings.Contains(rendered, "⬢ Buscando fuentes") {
		t.Fatalf("renderSearchDiagnostics() = %q, want archived task list preserved after first token", rendered)
	}
	if strings.Contains(rendered, "  ⊠ "+progressStepRewrite) || strings.Contains(rendered, "  ⊡ "+progressStepResponse) {
		t.Fatalf("renderSearchDiagnostics() = %q, want archived block to hide subtasks after first token", rendered)
	}
}

func TestHandleStreamProgressDoesNotReactivateCompactDiagnostics(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 80
	m.appendProgressBlock()
	block := &m.blocks[m.progressBlockIndex]
	now := time.Date(2026, time.April, 19, 10, 0, 0, 0, time.UTC)
	block.diag.startedAt = now
	block.diag.apply(search.ProgressUpdate{Key: progressKeyRewrite, State: search.ProgressPending}, now)
	block.diag.apply(search.ProgressUpdate{Key: progressKeyRewrite, State: search.ProgressDone}, now.Add(1*time.Second))
	block.diag.apply(search.ProgressUpdate{Key: progressKeyChunking, State: search.ProgressPending}, now.Add(2*time.Second))
	block.diag.apply(search.ProgressUpdate{Key: progressKeyChunking, State: search.ProgressDone}, now.Add(3*time.Second))
	block.diag.apply(search.ProgressUpdate{Key: progressKeyLLM, State: search.ProgressPending}, now.Add(4*time.Second))
	m.markSearchResponseStarted()

	m.handleStreamProgress(streamProgressMsg{update: search.ProgressUpdate{Key: search.CachePersistKey(), State: search.ProgressDone}})

	rendered := m.renderSearchDiagnostics(block.diag, time.Now())
	if !strings.Contains(rendered, "⬢ Buscando fuentes") || !strings.Contains(rendered, "⬢ Procesando fuentes") || !strings.Contains(rendered, "⬢ "+progressStepResponse) {
		t.Fatalf("renderSearchDiagnostics() = %q, want compact archived task headers preserved", rendered)
	}
	if strings.Contains(rendered, "  ⊠ Guardando cache semantica") || strings.Contains(rendered, "  ⊡ "+progressStepResponse) || strings.Contains(rendered, "  ⊠ "+progressStepRewrite) {
		t.Fatalf("renderSearchDiagnostics() = %q, want late progress updates ignored once diagnostics are compact", rendered)
	}
	if !block.diag.compact {
		t.Fatal("expected diagnostics to remain compact after late progress update")
	}
}

func TestRefreshLLMTimerDisplayUpdatesStatusAndProgress(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.appendProgressBlock()
	m.llmTimerActive = true
	m.llmTimerStartedAt = time.Now().Add(-3 * time.Second)
	m.llmTimerPhase = "Consultando Ollama"

	m.refreshLLMTimerDisplay()

	if !strings.Contains(m.status, requestingStatus) {
		t.Fatalf("status = %q, want llm timer status", m.status)
	}
	if !strings.Contains(m.status, "s)") {
		t.Fatalf("status = %q, want elapsed seconds", m.status)
	}
	found := false
	for _, progress := range m.blocks[m.progressBlockIndex].progress {
		if progress.Key == "llm-elapsed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected llm elapsed progress line")
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
		t.Fatalf(wantNilCmdMessage, cmd)
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
		t.Fatalf(wantNilCmdMessage, cmd)
	}
	if m.status != "No hay respuesta para copiar todavia." {
		t.Fatalf("status = %q, want missing response message", m.status)
	}
}

func TestHandleKeyMsgClearsConversationAndSession(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.blocks = []messageBlock{
		{role: "user", raw: "hola", rendered: "hola"},
		{role: "assistant", raw: assistantResponse, rendered: assistantResponse},
		{role: "progress", rendered: "Tokens 2k", progress: []search.ProgressUpdate{{Key: "token-estimate", Text: "Tokens 2k", State: search.ProgressInfo}}},
	}
	m.session = []ollama.ChatMessage{{Role: "user", Content: "hola"}, {Role: "assistant", Content: assistantResponse}}
	m.activeBlockIndex = 1
	m.progressBlockIndex = 2
	m.pendingUserInput = pendingUserPrompt
	m.spinnerVisible = true
	m.userCanceled = true
	m.llmTimerActive = true
	m.llmTimerStartedAt = time.Now()
	m.llmTimerPhase = "Consultando Ollama"
	m.state = stateComplete
	m.streamCh = make(chan streamEvent)
	_, m.cancel = context.WithCancel(context.Background())
	m.refreshViewport()

	handled, cmd := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlL})

	if !handled {
		t.Fatal("handleKeyMsg() should handle ctrl+l")
	}
	if cmd != nil {
		t.Fatalf(wantNilCmdMessage, cmd)
	}
	if len(m.blocks) != 0 {
		t.Fatalf("blocks len = %d, want 0", len(m.blocks))
	}
	if len(m.session) != 0 {
		t.Fatalf("session len = %d, want 0", len(m.session))
	}
	if m.activeBlockIndex != -1 {
		t.Fatalf("activeBlockIndex = %d, want -1", m.activeBlockIndex)
	}
	if m.progressBlockIndex != -1 {
		t.Fatalf("progressBlockIndex = %d, want -1", m.progressBlockIndex)
	}
	if m.pendingUserInput != "" {
		t.Fatalf(wantEmptyPendingUserInput, m.pendingUserInput)
	}
	if m.state != stateReady {
		t.Fatalf("state = %q, want %q", m.state, stateReady)
	}
	if m.llmTimerActive {
		t.Fatal("llmTimerActive = true, want false")
	}
	if m.status != "Mensajes eliminados." {
		t.Fatalf("status = %q, want clear confirmation", m.status)
	}
	if got := strings.TrimSpace(m.conversationContent()); got != "" {
		t.Fatalf("conversationContent() = %q, want empty", got)
	}
	if m.streamCh != nil {
		t.Fatal("streamCh should be cleared")
	}
	if m.cancel != nil {
		t.Fatal("cancel should be cleared")
	}
}

func TestHandleKeyMsgDoesNotClearConversationWhileRequesting(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.blocks = []messageBlock{{role: "assistant", raw: assistantResponse, rendered: assistantResponse}}
	m.session = []ollama.ChatMessage{{Role: "assistant", Content: assistantResponse}}
	m.requesting = true
	m.status = requestingStatus

	handled, cmd := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlL})

	if !handled {
		t.Fatal("handleKeyMsg() should handle ctrl+l while requesting")
	}
	if cmd != nil {
		t.Fatalf(wantNilCmdMessage, cmd)
	}
	if len(m.blocks) != 1 {
		t.Fatalf("blocks len = %d, want 1", len(m.blocks))
	}
	if len(m.session) != 1 {
		t.Fatalf("session len = %d, want 1", len(m.session))
	}
	if m.status != requestingStatus {
		t.Fatalf("status = %q, want unchanged requesting status", m.status)
	}
}

func TestHandleKeyMsgOpensSourceSelectionWithCtrlS(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 72
	m.state = stateComplete
	m.lastSearchDocs = []search.Document{{Title: "Fuente A", URL: testSourceURLA}, {Title: "Fuente B", URL: testSourceURLB}}

	handled, cmd := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlS})

	if !handled {
		t.Fatal("handleKeyMsg() should handle ctrl+s")
	}
	if cmd != nil {
		t.Fatalf(wantNilCmdMessage, cmd)
	}
	if m.state != stateSourceSelect {
		t.Fatalf("state = %q, want %q", m.state, stateSourceSelect)
	}
	if m.sourceMode != sourceModeSelecting {
		t.Fatalf("sourceMode = %q, want %q", m.sourceMode, sourceModeSelecting)
	}
	normalized := strings.Join(strings.Fields(stripANSISequences(m.mainViewportContent())), " ")
	if !strings.Contains(normalized, "SELECCION DE FUENTES") || !strings.Contains(normalized, "Fuente A") || !strings.Contains(normalized, "Fuente B") {
		t.Fatalf("mainViewportContent() = %q, want source selection content", stripANSISequences(m.mainViewportContent()))
	}
}

func TestHandleKeyMsgSourceSelectionLoadsChosenSource(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 72
	var gotURL string
	m.searchBuilder = &stubSearchBuilder{
		fetchSource: func(ctx context.Context, sourceURL string, title string, onActivity func(), onProgress func(search.ProgressUpdate)) (search.SourceDocument, error) {
			gotURL = sourceURL
			return search.SourceDocument{
				Document: search.Document{Title: title, URL: sourceURL, Content: "contenido limpio"},
				Markdown: "# " + title + "\n\ncontenido limpio",
			}, nil
		},
	}
	m.state = stateSourceSelect
	m.sourceMode = sourceModeSelecting
	m.lastSearchDocs = []search.Document{{Title: "Fuente A", URL: testSourceURLA}}

	handled, cmd := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if !handled {
		t.Fatal("handleKeyMsg() should handle numeric source selection")
	}
	if cmd == nil {
		t.Fatal("handleKeyMsg() returned nil cmd for source selection")
	}

	msg := cmd()
	updatedModel, _ := m.Update(msg)
	updated := updatedModel.(model)

	if gotURL != testSourceURLA {
		t.Fatalf("FetchSource() url = %q, want %q", gotURL, testSourceURLA)
	}
	if updated.state != stateSourceView {
		t.Fatalf("state = %q, want %q", updated.state, stateSourceView)
	}
	if updated.sidebarWidth() <= 0 {
		t.Fatalf("sidebarWidth() = %d, want visible sidebar in source view", updated.sidebarWidth())
	}
	if updated.sourceDocument == nil || !strings.Contains(updated.sourceDocument.Markdown, "contenido limpio") {
		t.Fatalf("sourceDocument = %#v, want loaded markdown", updated.sourceDocument)
	}
	normalizedMain := strings.Join(strings.Fields(stripANSISequences(updated.mainViewportContent())), " ")
	if !strings.Contains(normalizedMain, "contenido limpio") {
		t.Fatalf("mainViewportContent() = %q, want loaded source markdown", stripANSISequences(updated.mainViewportContent()))
	}
	normalizedSidebar := strings.Join(strings.Fields(stripANSISequences(updated.sidebarContent())), " ")
	if !strings.Contains(strings.ToLower(normalizedSidebar), "preguntas sobre la fuente") {
		t.Fatalf("sidebarContent() = %q, want source question placeholder", stripANSISequences(updated.sidebarContent()))
	}
}

func TestHandleKeyMsgCtrlFOpensSourceSearchModal(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.state = stateSourceView
	m.sourceMode = sourceModeViewing
	m.sourceDocument = &search.SourceDocument{
		Document: search.Document{Title: "Fuente A", URL: testSourceURLA},
		Markdown: "# Fuente A\n\ncontenido limpio",
	}

	handled, cmd := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlF})

	if !handled {
		t.Fatal("handleKeyMsg() should handle ctrl+f in source view")
	}
	if cmd != nil {
		t.Fatalf(wantNilCmdMessage, cmd)
	}
	if !m.sourceSearchModalOpen {
		t.Fatal("sourceSearchModalOpen = false, want true")
	}
	if !m.sourceSearchInput.Focused() {
		t.Fatal("sourceSearchInput should be focused")
	}
}

func TestHandleKeyMsgEnterExecutesSourceSearchAndHighlightsMatches(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.width = 100
	m.height = 24
	m.state = stateSourceView
	m.sourceMode = sourceModeViewing
	m.sourceDocument = &search.SourceDocument{
		Document: search.Document{Title: "Fuente A", URL: testSourceURLA},
		Markdown: "# Fuente A\n\nalpha beta\n\ngamma alpha\n\nalpha end",
	}
	m.syncPaneLayout()

	handled, _ := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlF})
	if !handled {
		t.Fatal("handleKeyMsg() should open source search modal")
	}
	m.sourceSearchInput.SetValue("alpha")

	handled, cmd := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled {
		t.Fatal("handleKeyMsg() should handle enter in source search modal")
	}
	if cmd != nil {
		t.Fatalf(wantNilCmdMessage, cmd)
	}
	if m.sourceSearchModalOpen {
		t.Fatal("sourceSearchModalOpen = true, want closed after enter")
	}
	if len(m.sourceSearchMatches) != 3 {
		t.Fatalf("sourceSearchMatches len = %d, want 3", len(m.sourceSearchMatches))
	}
	if m.sourceSearchCurrent != 0 {
		t.Fatalf("sourceSearchCurrent = %d, want 0", m.sourceSearchCurrent)
	}
	if m.status != "3 resultados encontrados." {
		t.Fatalf("status = %q, want results count", m.status)
	}

	rendered := m.mainViewportContent()
	if !strings.Contains(rendered, "\x1b[38;2;24;24;24;48;2;160;231;252m") {
		t.Fatalf("mainViewportContent() = %q, want all-match highlight colors", rendered)
	}
	if !strings.Contains(rendered, "\x1b[38;2;228;238;245;48;2;10;98;200m") {
		t.Fatalf("mainViewportContent() = %q, want current-match highlight colors", rendered)
	}
}

func TestSourceSearchNavigationWrapsNextAndPrevious(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.width = 100
	m.height = 24
	m.state = stateSourceView
	m.sourceMode = sourceModeViewing
	m.sourceDocument = &search.SourceDocument{
		Document: search.Document{Title: "Fuente A", URL: testSourceURLA},
		Markdown: "# Fuente A\n\nuno\n\ndos\n\nuno\n\ntres\n\nuno",
	}
	m.syncPaneLayout()
	m.executeSourceSearch("uno")

	if len(m.sourceSearchMatches) != 3 {
		t.Fatalf("sourceSearchMatches len = %d, want 3", len(m.sourceSearchMatches))
	}

	handled, _ := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlN})
	if !handled {
		t.Fatal("handleKeyMsg() should handle ctrl+n in source view")
	}
	if m.sourceSearchCurrent != 1 {
		t.Fatalf("sourceSearchCurrent = %d, want 1", m.sourceSearchCurrent)
	}

	_, _ = m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlN})
	_, _ = m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlN})
	if m.sourceSearchCurrent != 0 {
		t.Fatalf("sourceSearchCurrent = %d, want wrap to 0", m.sourceSearchCurrent)
	}

	m.cycleSourceSearch(-1)
	if m.sourceSearchCurrent != 2 {
		t.Fatalf("sourceSearchCurrent = %d, want wrap to last index", m.sourceSearchCurrent)
	}
}

func TestHandleKeyMsgEnterInSourceViewRoutesAnswerToSidebar(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(jsonContentTypeHeader, jsonContentTypeValue)
		_, _ = w.Write([]byte("{\"message\":{\"content\":\"respuesta lateral\"}}\n"))
		_, _ = w.Write([]byte(doneChunkPayload))
	}))
	defer server.Close()

	m := newModel(config.Config{OllamaURL: server.URL, Model: "gemma4"}, "")
	m.viewport.Width = 72
	m.sidebar.Width = 28
	m.client = ollama.NewClient(server.URL, "gemma4")
	m.state = stateSourceView
	m.sourceMode = sourceModeViewing
	m.sourceDocument = &search.SourceDocument{
		Document: search.Document{Title: "Fuente A", URL: testSourceURLA, Content: "contenido limpio"},
		Markdown: "# Fuente A\n\ncontenido limpio",
	}
	m.input.SetValue("que dice esta fuente?")

	handled, cmd := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled {
		t.Fatal("handleKeyMsg() should handle enter in source view")
	}
	if cmd == nil {
		t.Fatal("handleKeyMsg() returned nil cmd for source question")
	}
	if len(m.sidebarTurns) != 1 || m.sidebarTurns[0].role != "user" {
		t.Fatalf("sidebarTurns = %#v, want first user turn queued", m.sidebarTurns)
	}

	msg := cmd()
	updatedModel, _ := m.Update(msg)
	updated := updatedModel.(model)

	if len(updated.sidebarTurns) != 2 {
		t.Fatalf("sidebarTurns len = %d, want 2", len(updated.sidebarTurns))
	}
	if updated.sidebarTurns[1].role != "assistant" || updated.sidebarTurns[1].content != "respuesta lateral" {
		t.Fatalf("sidebarTurns[1] = %#v, want assistant sidebar response", updated.sidebarTurns[1])
	}
	normalizedSidebar := strings.Join(strings.Fields(stripANSISequences(updated.sidebarContent())), " ")
	if !strings.Contains(normalizedSidebar, "respuesta lateral") {
		t.Fatalf("sidebarContent() = %q, want assistant answer rendered", stripANSISequences(updated.sidebarContent()))
	}
}

func TestHandleKeyMsgShiftArrowsScrollSidebarInSourceView(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.width = 120
	m.height = 24
	m.state = stateSourceView
	m.sourceMode = sourceModeViewing
	m.sourceDocument = &search.SourceDocument{
		Document: search.Document{Title: "Fuente A", URL: testSourceURLA},
		Markdown: "# Fuente A\n\ncontenido limpio",
	}
	m.sidebarTurns = []sourceSidebarTurn{
		{role: "assistant", content: "linea 1\n\nlinea 2\n\nlinea 3\n\nlinea 4\n\nlinea 5\n\nlinea 6"},
	}
	m.syncPaneWidths()
	m.syncViewportLayout()
	m.sidebar.Height = 3
	m.refreshSidebar()
	m.sidebar.GotoTop()

	handled, cmd := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyShiftDown})
	if !handled {
		t.Fatal("handleKeyMsg() should handle shift+down in source view")
	}
	if cmd != nil {
		t.Fatalf(wantNilCmdMessage, cmd)
	}
	if m.sidebar.YOffset <= 0 {
		t.Fatalf("sidebar.YOffset = %d, want sidebar scrolled down", m.sidebar.YOffset)
	}

	handled, cmd = m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyShiftUp})
	if !handled {
		t.Fatal("handleKeyMsg() should handle shift+up in source view")
	}
	if cmd != nil {
		t.Fatalf(wantNilCmdMessage, cmd)
	}
	if m.sidebar.YOffset != 0 {
		t.Fatalf("sidebar.YOffset = %d, want sidebar returned to top", m.sidebar.YOffset)
	}
}

func TestHandleKeyMsgCtrlCClosesSourceMode(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.state = stateSourceView
	m.sourceMode = sourceModeViewing
	m.sourcePreviousState = stateComplete
	m.sourceDocument = &search.SourceDocument{Document: search.Document{Title: "Fuente A", URL: testSourceURLA}, Markdown: "# Fuente A"}
	m.sidebarTurns = []sourceSidebarTurn{{role: "assistant", content: "respuesta lateral"}}

	handled, cmd := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlC})

	if !handled {
		t.Fatal("handleKeyMsg() should handle ctrl+c in source mode")
	}
	if cmd != nil {
		t.Fatalf(wantNilCmdMessage, cmd)
	}
	if m.state != stateComplete {
		t.Fatalf("state = %q, want %q", m.state, stateComplete)
	}
	if m.sourceDocument != nil {
		t.Fatalf("sourceDocument = %#v, want cleared source state", m.sourceDocument)
	}
	if len(m.sidebarTurns) != 0 {
		t.Fatalf("sidebarTurns = %#v, want cleared sidebar turns", m.sidebarTurns)
	}
}

func TestCloseSourceModeRestoresFullWidthConversationRendering(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 20})
	m.blocks = []messageBlock{{role: "assistant", raw: assistantResponse, rendered: assistantResponse}}
	m.activeBlockIndex = 0
	m.state = stateSourceView
	m.sourceMode = sourceModeViewing
	m.sourcePreviousState = stateComplete
	m.sourceDocument = &search.SourceDocument{
		Document: search.Document{Title: "Fuente A", URL: testSourceURLA},
		Markdown: "# Fuente A\n\ncontenido limpio",
	}
	m.syncPaneLayout()

	sourceWidth := m.viewport.Width
	if sourceWidth >= m.outerWidth() {
		t.Fatalf("viewport.Width in source view = %d, want split layout narrower than outer width %d", sourceWidth, m.outerWidth())
	}

	m.closeSourceMode()

	if m.viewport.Width != m.outerWidth() {
		t.Fatalf("viewport.Width after close = %d, want full width %d", m.viewport.Width, m.outerWidth())
	}
	if m.sidebarWidth() != 0 {
		t.Fatalf("sidebarWidth() after close = %d, want hidden sidebar", m.sidebarWidth())
	}
}

func TestHandleKeyMsgOpensCurrentInputInConfiguredEditor(t *testing.T) {
	m := newModel(config.Config{Editor: "vscode"}, "")
	m.blocks = []messageBlock{{role: "assistant", raw: assistantResponse, rendered: assistantResponse}}
	m.activeBlockIndex = 0
	m.input.SetValue("prompt original")

	var gotEditor string
	var gotContent string
	m.openInEditor = func(_ *i18n.Localizer, editor, content string) tea.Cmd {
		gotEditor = editor
		gotContent = content
		return func() tea.Msg {
			return editorDoneMsg{content: content + " editada", editorLabel: "Visual Studio Code"}
		}
	}

	handled, cmd := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlE})

	if !handled {
		t.Fatal("handleKeyMsg() should handle ctrl+e")
	}
	if cmd == nil {
		t.Fatal("handleKeyMsg() cmd = nil, want editor command")
	}
	if gotEditor != "vscode" {
		t.Fatalf("editor = %q, want vscode", gotEditor)
	}
	if gotContent != "prompt original" {
		t.Fatalf("content = %q, want current input", gotContent)
	}

	updated, nextCmd := m.Update(cmd())
	if nextCmd != nil {
		t.Fatalf("Update() cmd = %v, want nil", nextCmd)
	}

	result, ok := updated.(model)
	if !ok {
		t.Fatalf("Update() model type = %T, want model", updated)
	}
	if result.input.Value() != "prompt original editada" {
		t.Fatalf("input.Value() = %q, want edited content", result.input.Value())
	}
	if result.lastAssistant() != assistantResponse {
		t.Fatalf("lastAssistant() = %q, want original assistant response", result.lastAssistant())
	}
	if result.status != "Input actualizado desde Visual Studio Code." {
		t.Fatalf("status = %q, want editor confirmation", result.status)
	}
}

func TestHandleKeyMsgOpensEditorWithEmptyInput(t *testing.T) {
	m := newModel(config.Config{}, "")

	var gotEditor string
	var gotContent string
	m.openInEditor = func(_ *i18n.Localizer, editor, content string) tea.Cmd {
		gotEditor = editor
		gotContent = content
		return func() tea.Msg {
			return editorDoneMsg{content: "texto nuevo", editorLabel: "Neovim"}
		}
	}

	handled, cmd := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlE})

	if !handled {
		t.Fatal("handleKeyMsg() should handle ctrl+e")
	}
	if cmd == nil {
		t.Fatal("handleKeyMsg() cmd = nil, want editor command")
	}
	if gotEditor != "neovim" {
		t.Fatalf("editor = %q, want neovim", gotEditor)
	}
	if gotContent != "" {
		t.Fatalf("content = %q, want empty input", gotContent)
	}

	updated, nextCmd := m.Update(cmd())
	if nextCmd != nil {
		t.Fatalf("Update() cmd = %v, want nil", nextCmd)
	}

	result, ok := updated.(model)
	if !ok {
		t.Fatalf("Update() model type = %T, want model", updated)
	}
	if result.input.Value() != "texto nuevo" {
		t.Fatalf("input.Value() = %q, want edited content", result.input.Value())
	}
	if result.status != "Input actualizado desde Neovim." {
		t.Fatalf("status = %q, want editor confirmation", result.status)
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

	if !strings.Contains(got, "pregunta\n\nrespuesta") {
		t.Fatalf("conversationContent() = %q, want one blank line between user and assistant message", got)
	}
	if lipgloss.Width(separator) != 12 {
		t.Fatalf("separator width = %d, want 12", lipgloss.Width(separator))
	}
	if strings.Contains(got, separator) {
		t.Fatalf("conversationContent() = %q, want no separators in the updated layout", got)
	}
}

func TestConversationViewportViewDoesNotAddLeadingBlankLine(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 20
	m.viewport.Height = 8
	m.blocks = []messageBlock{{role: "user", rendered: "pregunta"}}
	m.refreshViewport()

	rendered := m.conversationViewportView()
	if strings.HasPrefix(rendered, "\n") {
		t.Fatalf("conversationViewportView() = %q, want no leading blank line added by the viewport", rendered)
	}
	if !strings.Contains(rendered, "pregunta") {
		t.Fatalf("conversationViewportView() = %q, want conversation content", rendered)
	}
}

func TestConversationViewportViewDoesNotAddBlankLineAboveUserAfterAssistantResponse(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 20
	m.viewport.Height = 8
	m.blocks = []messageBlock{
		{role: "user", rendered: "pregunta"},
		{role: "assistant", rendered: "respuesta"},
	}
	m.refreshViewport()

	rendered := m.conversationViewportView()
	if strings.HasPrefix(rendered, "\n") {
		t.Fatalf("conversationViewportView() = %q, want no extra leading blank line after assistant response", rendered)
	}
	lines := strings.Split(rendered, "\n")
	if len(lines) < 4 {
		t.Fatalf("conversationViewportView() = %q, want enough lines for padding and both blocks", rendered)
	}
	if strings.TrimSpace(lines[0]) != "pregunta" {
		t.Fatalf("conversationViewportView() first line = %q, want user block without extra line above", lines[0])
	}
	if strings.TrimSpace(lines[1]) != "" {
		t.Fatalf("conversationViewportView() second line = %q, want one blank line between user and assistant", lines[1])
	}
	if strings.TrimSpace(lines[2]) != "respuesta" {
		t.Fatalf("conversationViewportView() third line = %q, want assistant block immediately after the separator", lines[2])
	}
	if strings.TrimSpace(lines[3]) != "" {
		t.Fatalf("conversationViewportView() fourth line = %q, want remaining assistant block bottom padding only", lines[3])
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

func TestRenderInputViewDoesNotPanicAfterUpWithoutMatchingSuggestions(t *testing.T) {
	m := newModel(config.Config{Commands: map[string]config.SlashCommand{
		"fix": {Template: fixTemplate},
	}}, "")
	m.viewport.Width = 24
	m.input.Width = m.inputContentWidth()
	m.input.SetValue("texto libre")
	m.input.CursorEnd()

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(tea.KeyMsg{Type: tea.KeyUp})
	if cmd != nil {
		_ = cmd
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("renderInputView() panicked after up without matches: %v", recovered)
		}
	}()

	rendered := m.renderInputView()
	if !strings.Contains(rendered, "texto libre") {
		t.Fatalf("renderInputView() = %q, want input text preserved", rendered)
	}
}

func TestViewDoesNotRenderHeaderAndFooterHelp(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 30
	m.viewport.Height = 8
	m.refreshViewport()

	rendered := m.View()
	if strings.Contains(rendered, "# sparkle-cli") {
		t.Fatalf("View() = %q, want header hidden", rendered)
	}
	if strings.Contains(rendered, "Enter enviar") {
		t.Fatalf("View() = %q, want footer help hidden", rendered)
	}
}

func TestFooterHelpTextReturnsEmpty(t *testing.T) {
	cfg := config.Config{Commands: map[string]config.SlashCommand{
		"fix":    {Template: fixTemplate},
		"ticket": {Template: ticketTemplate},
	}}
	m := newModel(cfg, "")

	if got := m.footerHelpText(); got != "" {
		t.Fatalf("footerHelpText() = %q, want empty", got)
	}
}

func TestViewDoesNotLeaveBottomPaddingAfterFooter(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 30
	m.refreshViewport()

	rendered := stripANSISequences(m.View())
	lines := strings.Split(rendered, "\n")
	lastLine := strings.TrimSpace(lines[len(lines)-1])
	if lastLine == "" {
		t.Fatalf("View() = %q, want footer to be the last visible line without bottom padding", rendered)
	}
}

func TestViewSlashCommandsFooterHasNoExtraIndentation(t *testing.T) {
	cfg := config.Config{Commands: map[string]config.SlashCommand{
		"fix":    {Template: fixTemplate},
		"ticket": {Template: ticketTemplate},
	}}
	m := newModel(cfg, "")
	m.handleWindowSize(tea.WindowSizeMsg{Width: 60, Height: 12})

	rendered := stripANSISequences(m.View())
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "slash commands") {
			t.Fatalf("View() = %q, want no slash footer help line", rendered)
		}
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

func TestHandleWindowSizeShrinksViewportWhenFooterWraps(t *testing.T) {
	cfg := config.Config{Commands: map[string]config.SlashCommand{
		"fix":    {Template: fixTemplate},
		"ticket": {Template: ticketTemplate},
	}}
	m := newModel(cfg, "")
	m.status = "Estado visible para probar el layout"
	m.input.SetValue("este input necesita dos lineas para validar el calculo real")
	m.input.CursorEnd()

	msg := tea.WindowSizeMsg{Width: 48, Height: 14}
	m.handleWindowSize(msg)

	if reserved := m.layoutReservedHeight(); reserved <= 4 {
		t.Fatalf("layoutReservedHeight() = %d, want > 4 when input and status wrap", reserved)
	}
	wantHeight := msg.Height - m.layoutReservedHeight()
	if wantHeight < 0 {
		wantHeight = 0
	}
	if got, want := m.viewport.Height, wantHeight; got != want {
		t.Fatalf("viewport.Height = %d, want %d", got, want)
	}

	conversationBody := m.fillLinesWithBackground(m.conversationViewportView(), m.outerWidth(), m.colors.bgBase)
	conversation := m.styles.conversation.Width(m.outerWidth()).Render(conversationBody)
	inputBody := m.fillLinesWithBackground(m.renderInputView(), m.inputContentWidth(), m.colors.bgRaised)
	input := m.styles.inputBox.Width(m.outerWidth()).Render(inputBody)
	sections := []string{conversation, m.renderStatusLine(), input}
	body := lipgloss.JoinVertical(lipgloss.Left, sections...)
	frame := stripANSISequences(m.styles.frame.Render(body))

	if got := lipgloss.Height(frame); got <= 0 || got > msg.Height {
		t.Fatalf("frame height = %d, want between 1 and %d", got, msg.Height)
	}
	if strings.Contains(frame, "slash commands") {
		t.Fatalf("frame = %q, want footer help hidden", frame)
	}
}

func TestCopyStatusLineShrinksViewportImmediately(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.blocks = []messageBlock{{role: "assistant", raw: assistantResponse, rendered: assistantResponse}}
	m.activeBlockIndex = 0
	m.handleWindowSize(tea.WindowSizeMsg{Width: 60, Height: 14})

	initialHeight := m.viewport.Height
	m.clipboardWrite = func(value string) error { return nil }
	m.copyLatestAssistant()

	if got, want := m.viewport.Height, m.availableConversationHeight(m.height); got != want {
		t.Fatalf("viewport.Height = %d, want %d after visible status", got, want)
	}
	if m.viewport.Height >= initialHeight {
		t.Fatalf("viewport.Height = %d, want less than %d after visible status", m.viewport.Height, initialHeight)
	}
}

func TestHandleStreamDoneRestoresViewportHeightWhenStatusLineDisappears(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.handleWindowSize(tea.WindowSizeMsg{Width: 60, Height: 14})
	m.requesting = true
	m.activeBlockIndex = -1
	m.setStatus(requestingStatus)
	withStatus := m.viewport.Height

	m.handleStreamDone()

	if got, want := m.viewport.Height, m.availableConversationHeight(m.height); got != want {
		t.Fatalf("viewport.Height = %d, want %d after hidden status", got, want)
	}
	if m.viewport.Height <= withStatus {
		t.Fatalf("viewport.Height = %d, want greater than %d after hidden status", m.viewport.Height, withStatus)
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
	plainRendered := stripANSISequences(rendered)
	if !strings.Contains(plainRendered, slashCommandLabel(slashCommandFix)) {
		t.Fatalf("renderUserBlockContent() = %q, want slash command label visible after wrapping", rendered)
	}
	if !strings.Contains(plainRendered, "") || !strings.Contains(plainRendered, "") {
		t.Fatalf("renderUserBlockContent() = %q, want slash command pill separators after wrapping", rendered)
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

func TestStartRequestUsesDefaultModelWhenCommandHasNoModelOverride(t *testing.T) {
	modelCh := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()

		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		modelCh <- request.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"done\":true}\n"))
	}))
	defer server.Close()

	cfg := config.Config{
		OllamaURL: server.URL,
		Model:     "gemma4",
		Timeout:   1,
		Commands: map[string]config.SlashCommand{
			"ticket": {Template: "Crear ticket {{.Priority}}: {{.Input}}", Params: []string{"priority"}},
		},
	}
	m := newModel(cfg, "")
	m.client = ollama.NewClient(server.URL, cfg.Model)

	cmd := m.startRequest("/ticket priority=alta hola mundo")
	if cmd == nil {
		t.Fatal("startRequest() returned nil cmd")
	}

	select {
	case got := <-modelCh:
		if got != "gemma4" {
			t.Fatalf("ollama model = %q, want gemma4", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Ollama request")
	}

	if m.cancel != nil {
		m.cancel()
	}
}

func TestStartRequestSearchWarmsModelsImmediately(t *testing.T) {
	type modelOnlyPayload struct {
		Model    string            `json:"model"`
		Messages []json.RawMessage `json:"messages"`
		Prompt   string            `json:"prompt"`
	}

	preloadModelCh := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()

		var request modelOnlyPayload
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		switch r.URL.Path {
		case "/api/chat":
			if len(request.Messages) == 0 && strings.TrimSpace(request.Prompt) == "" {
				preloadModelCh <- strings.TrimSpace(request.Model)
			}
			w.Header().Set(jsonContentTypeHeader, jsonContentTypeValue)
			_, _ = w.Write([]byte(doneChunkPayload))
		case "/api/generate":
			if strings.TrimSpace(request.Prompt) == "" {
				preloadModelCh <- strings.TrimSpace(request.Model)
			}
			w.Header().Set(jsonContentTypeHeader, jsonContentTypeValue)
			_, _ = w.Write([]byte(`{"done":true}`))
		case "/api/embed", "/api/embeddings":
			t.Fatalf("unexpected embedding preload path: %s", r.URL.Path)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg := config.Config{
		OllamaURL: server.URL,
		Model:     "gemma4",
		Commands: map[string]config.SlashCommand{
			"search": {Template: "{{.Input}}", Kind: slash.KindSearch},
		},
	}
	m := newModel(cfg, "")
	m.client = ollama.NewClient(server.URL, cfg.Model)

	cmd := m.startRequest("/search como instalar ollama")
	if cmd == nil {
		t.Fatal("startRequest() returned nil cmd")
	}

	select {
	case modelName := <-preloadModelCh:
		if modelName != "gemma4" {
			t.Fatalf("preload model = %q, want gemma4", modelName)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting warmup model-only preload call")
	}

	if m.cancel != nil {
		m.cancel()
	}
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

func TestStartIdleTimeoutWatcherExpiresWithoutActivity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, timedOut, stop := startIdleTimeoutWatcher(ctx, 20*time.Millisecond, cancel)
	defer stop()

	select {
	case <-ctx.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout watcher did not cancel context")
	}

	if !timedOut() {
		t.Fatal("expected timeout watcher to report timeout")
	}
}

func TestStartIdleTimeoutWatcherResetsOnActivity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	touch, timedOut, stop := startIdleTimeoutWatcher(ctx, 30*time.Millisecond, cancel)
	defer stop()

	for range 3 {
		time.Sleep(10 * time.Millisecond)
		touch()
		select {
		case <-ctx.Done():
			t.Fatal("context canceled before inactivity timeout elapsed")
		default:
		}
	}

	time.Sleep(15 * time.Millisecond)
	select {
	case <-ctx.Done():
		t.Fatal("context canceled while activity was still refreshing the timeout")
	default:
	}

	select {
	case <-ctx.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("context was not canceled after activity stopped")
	}

	if !timedOut() {
		t.Fatal("expected timeout watcher to report timeout after inactivity")
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

func TestRenderMarkdownContentRemovesLiteralHeadingMarkers(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 60
	m.renderer, _ = newMarkdownRenderer(m.colors, m.viewport.Width)

	rendered := stripANSISequences(m.renderMarkdownContent("### 🧠 Conceptos Clave Explicados\n\nTexto."))

	if strings.Contains(rendered, "### 🧠 Conceptos Clave Explicados") {
		t.Fatalf("renderMarkdownContent() = %q, want heading rendered without literal markdown markers", rendered)
	}
	if !strings.Contains(rendered, "🧠 CONCEPTOS CLAVE EXPLICADOS") {
		t.Fatalf("renderMarkdownContent() = %q, want heading emphasized in uppercase", rendered)
	}
	if !strings.Contains(rendered, "Texto.") {
		t.Fatalf("renderMarkdownContent() = %q, want paragraph content preserved", rendered)
	}
}

func TestHandleKeyMsgTogglesThinkingMode(t *testing.T) {
	m := newModel(config.Config{}, "")

	handled, cmd := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlT})

	if !handled {
		t.Fatal("handleKeyMsg() should handle ctrl+t")
	}
	if cmd != nil {
		t.Fatalf(wantNilCmdMessage, cmd)
	}
	if m.mode != modeReasoning {
		t.Fatalf("mode = %q, want %q after first ctrl+t", m.mode, modeReasoning)
	}
	if got := m.modeLabel(); got != m.localizer.Get("mode.reasoning") {
		t.Fatalf("modeLabel() = %q, want %q", got, m.localizer.Get("mode.reasoning"))
	}

	handled, cmd = m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlT})
	if !handled {
		t.Fatal("handleKeyMsg() should handle second ctrl+t")
	}
	if cmd != nil {
		t.Fatalf(wantNilCmdMessage, cmd)
	}
	if m.mode != modeChat {
		t.Fatalf("mode = %q, want %q after second ctrl+t", m.mode, modeChat)
	}
	if got := m.modeLabel(); got != m.localizer.Get("mode.chat") {
		t.Fatalf("modeLabel() = %q, want %q", got, m.localizer.Get("mode.chat"))
	}

	handled, cmd = m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlT})
	if !handled {
		t.Fatal("handleKeyMsg() should handle third ctrl+t")
	}
	if cmd != nil {
		t.Fatalf(wantNilCmdMessage, cmd)
	}
	if m.mode != modeAgent {
		t.Fatalf("mode = %q, want %q after third ctrl+t", m.mode, modeAgent)
	}
	if got := m.modeLabel(); got != m.localizer.Get("mode.agent") {
		t.Fatalf("modeLabel() = %q, want %q", got, m.localizer.Get("mode.agent"))
	}

	handled, cmd = m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlT})
	if !handled {
		t.Fatal("handleKeyMsg() should handle fourth ctrl+t")
	}
	if cmd != nil {
		t.Fatalf(wantNilCmdMessage, cmd)
	}
	if m.mode != modeNormal {
		t.Fatalf("mode = %q, want %q after fourth ctrl+t", m.mode, modeNormal)
	}
}

func TestRunDirectReturnsOnlyFinalAnswerInReasoningMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(jsonContentTypeHeader, jsonContentTypeValue)
		_, _ = io.WriteString(w, "{\"message\":{\"content\":\"<think>analizando</think>respuesta final\"},\"done\":false}\n")
		_, _ = io.WriteString(w, doneChunkPayload)
	}))
	defer server.Close()

	got, err := RunDirect(config.Config{OllamaURL: server.URL, Model: "gemma4"}, "", "por que el cielo es azul", "thinking", "", nil)
	if err != nil {
		t.Fatalf("RunDirect() error = %v", err)
	}
	if got != assistantResponse {
		t.Fatalf("RunDirect() = %q, want %q", got, assistantResponse)
	}
	if strings.Contains(got, "analizando") {
		t.Fatalf("RunDirect() = %q, want no reasoning content", got)
	}
}

func TestRunDirectResolvesSearchSlashAndPersistsCache(t *testing.T) {
	m := newModel(config.Config{
		Model:    "gemma4",
		Commands: map[string]config.SlashCommand{"search": {Template: "{{.Input}}", Kind: slash.KindSearch}},
	}, "")
	persistCalled := false
	m.searchBuilder = &stubSearchBuilder{
		prepared: search.PreparedPrompt{
			Prompt:     finalPromptText,
			Query:      ollamaInstallQuestion,
			Documents:  []search.Document{{URL: testSourceURLA}},
			CacheQuery: ollamaInstallQuestion,
			CacheDocs:  []search.Document{{URL: testSourceURLA, Content: "contenido"}},
		},
		persist: func(query string, documents []search.Document, onProgress func(search.ProgressUpdate)) <-chan struct{} {
			persistCalled = query == ollamaInstallQuestion && len(documents) == 1
			done := make(chan struct{})
			close(done)
			return done
		},
	}
	m.client = ollama.NewClient("http://127.0.0.1:1", "gemma4")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(jsonContentTypeHeader, jsonContentTypeValue)
		_, _ = io.WriteString(w, "{\"message\":{\"content\":\"respuesta sin cita\"},\"done\":false}\n")
		_, _ = io.WriteString(w, doneChunkPayload)
	}))
	defer server.Close()
	m.client = ollama.NewClient(server.URL, "gemma4")

	got, err := runDirectWithModel(&m, "/search "+ollamaInstallQuestion, "normal")
	if err != nil {
		t.Fatalf("runDirectWithModel() error = %v", err)
	}
	if !strings.Contains(got, "respuesta sin cita") {
		t.Fatalf("runDirectWithModel() = %q, want answer body", got)
	}
	if !strings.Contains(got, sourcesFooterHeading) {
		t.Fatalf("runDirectWithModel() = %q, want synthetic sources footer", got)
	}
	if !persistCalled {
		t.Fatal("runDirectWithModel() did not persist semantic cache")
	}
}

func TestNormalizeDirectModeSupportsThinkingAlias(t *testing.T) {
	got, err := normalizeDirectMode("thinking")
	if err != nil {
		t.Fatalf("normalizeDirectMode() error = %v", err)
	}
	if got != modeReasoning {
		t.Fatalf("normalizeDirectMode() = %q, want %q", got, modeReasoning)
	}

	if _, err := normalizeDirectMode("chat"); err == nil {
		t.Fatal("normalizeDirectMode() error = nil, want error for chat")
	}
	if _, err := normalizeDirectMode("agent"); err == nil {
		t.Fatal("normalizeDirectMode() error = nil, want error for agent")
	}
}

func TestHandleKeyMsgTogglesReasoningVisibility(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 80
	m.appendBlock("assistant", "<think>razon interno</think>respuesta final")

	handled, cmd := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlK})
	if !handled {
		t.Fatal("handleKeyMsg() should handle ctrl+k")
	}
	if cmd != nil {
		t.Fatalf(wantNilCmdMessage, cmd)
	}
	if !m.reasoningExpanded {
		t.Fatal("reasoningExpanded = false, want true after first ctrl+k")
	}
	if m.status != m.localizer.Get("status.reasoning_expanded") {
		t.Fatalf("status = %q, want %q", m.status, m.localizer.Get("status.reasoning_expanded"))
	}
	expanded := stripANSISequences(m.blocks[0].rendered)
	if !strings.Contains(expanded, "razon interno") {
		t.Fatalf("expanded rendered content = %q, want raw reasoning content", expanded)
	}

	handled, cmd = m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlK})
	if !handled {
		t.Fatal("handleKeyMsg() should handle second ctrl+k")
	}
	if cmd != nil {
		t.Fatalf(wantNilCmdMessage, cmd)
	}
	if m.reasoningExpanded {
		t.Fatal("reasoningExpanded = true, want false after second ctrl+k")
	}
	if m.status != m.localizer.Get("status.reasoning_collapsed") {
		t.Fatalf("status = %q, want %q", m.status, m.localizer.Get("status.reasoning_collapsed"))
	}
	collapsed := stripANSISequences(m.blocks[0].rendered)
	if strings.Contains(collapsed, "razon interno") {
		t.Fatalf("collapsed rendered content = %q, want hidden reasoning text", collapsed)
	}
	if !strings.Contains(collapsed, "Razonando...") {
		t.Fatalf("collapsed rendered content = %q, want reasoning placeholder", collapsed)
	}
}

func TestHandleStreamChunkAdvancesReasoningPulseByToken(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.requesting = true

	m.handleStreamChunk(streamChunkMsg{content: "<think>p"})
	if m.reasoningPulseStep != 0 {
		t.Fatalf("reasoningPulseStep after first token = %d, want 0", m.reasoningPulseStep)
	}

	m.handleStreamChunk(streamChunkMsg{content: "a"})
	if m.reasoningPulseStep != 1 {
		t.Fatalf("reasoningPulseStep after second token = %d, want 1", m.reasoningPulseStep)
	}

	m.handleStreamChunk(streamChunkMsg{content: "</think>respuesta"})
	if m.reasoningPulseStep != -1 {
		t.Fatalf("reasoningPulseStep after closing reasoning = %d, want -1", m.reasoningPulseStep)
	}
}

func TestUpdateComponentsDoesNotScrollViewportOnRuneKeys(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 40
	m.viewport.Height = 4
	m.viewport.SetContent(strings.Repeat("linea\n", 40))
	m.viewport.GotoBottom()

	startOffset := m.viewport.YOffset

	m.updateComponents(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if m.viewport.YOffset != startOffset {
		t.Fatalf("YOffset after 'u' = %d, want %d", m.viewport.YOffset, startOffset)
	}

	m.updateComponents(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.viewport.YOffset != startOffset {
		t.Fatalf("YOffset after 'd' = %d, want %d", m.viewport.YOffset, startOffset)
	}
}

func TestUpdateComponentsScrollsViewportOnArrowKeys(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 40
	m.viewport.Height = 4
	m.viewport.SetContent(strings.Repeat("linea\n", 40))
	m.viewport.GotoBottom()

	startOffset := m.viewport.YOffset

	m.updateComponents(tea.KeyMsg{Type: tea.KeyUp})
	if m.viewport.YOffset >= startOffset {
		t.Fatalf("YOffset after up = %d, want less than %d", m.viewport.YOffset, startOffset)
	}

	afterUp := m.viewport.YOffset
	m.updateComponents(tea.KeyMsg{Type: tea.KeyDown})
	if m.viewport.YOffset <= afterUp {
		t.Fatalf("YOffset after down = %d, want greater than %d", m.viewport.YOffset, afterUp)
	}
}

func TestSplitThinkingOutputSeparatesThoughtFromAnswer(t *testing.T) {
	thought, answer, active := splitThinkingOutput("<|channel|>thought\nanalizando la solicitud<channel|>usa ls -la")

	if !active {
		t.Fatal("splitThinkingOutput() active = false, want true")
	}
	if thought != "analizando la solicitud" {
		t.Fatalf("thought = %q, want internal reasoning", thought)
	}
	if answer != "usa ls -la" {
		t.Fatalf("answer = %q, want final answer", answer)
	}
}

func TestSplitThinkingOutputHandlesEmptyThoughtBlock(t *testing.T) {
	thought, answer, active := splitThinkingOutput("<|channel|>thought\n<channel|>respuesta final")

	if !active {
		t.Fatal("splitThinkingOutput() active = false, want true")
	}
	if thought != "" {
		t.Fatalf("thought = %q, want empty thought", thought)
	}
	if answer != "respuesta final" {
		t.Fatalf("answer = %q, want final answer", answer)
	}
}

func TestRenderInputViewShowsThinkingIndicator(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.viewport.Width = 28
	m.input.Width = m.inputContentWidth()
	m.mode = modeReasoning

	rendered := m.renderInputView()

	if !strings.Contains(rendered, m.styles.modeIndicator.Render(m.localizer.Get("mode.reasoning"))) {
		t.Fatalf("renderInputView() = %q, want thinking indicator", rendered)
	}
	if strings.Contains(rendered, "─") {
		t.Fatalf("renderInputView() = %q, want no divider between the input and mode indicator", rendered)
	}
	if !strings.Contains(rendered, "\n\n") {
		t.Fatalf("renderInputView() = %q, want the mode indicator one line below the input", rendered)
	}
}

func TestBuildRequestMessagesUsesHistoryInChatAndAgentMode(t *testing.T) {
	m := newModel(config.Config{SystemPrompt: "sistema"}, "")
	m.session = []ollama.ChatMessage{
		{Role: "user", Content: "hola"},
		{Role: "assistant", Content: "buenas"},
	}

	normal := m.buildRequestMessages(followUpPrompt, "", m.cfg.Model)
	if len(normal) != 2 {
		t.Fatalf("normal messages len = %d, want 2", len(normal))
	}
	if normal[1].Content != followUpPrompt {
		t.Fatalf("normal user content = %q, want current prompt", normal[1].Content)
	}

	m.mode = modeChat
	chat := m.buildRequestMessages(followUpPrompt, "", m.cfg.Model)
	if len(chat) != 4 {
		t.Fatalf("chat messages len = %d, want 4", len(chat))
	}
	if chat[1].Content != "hola" || chat[2].Content != "buenas" || chat[3].Content != followUpPrompt {
		t.Fatalf("chat messages = %#v, want prior history plus current prompt", chat)
	}

	m.mode = modeAgent
	agent := m.buildRequestMessages(followUpPrompt, "", m.cfg.Model)
	if len(agent) != 4 {
		t.Fatalf("agent messages len = %d, want 4", len(agent))
	}
	if !strings.Contains(agent[0].Content, "Modo Agent") {
		t.Fatalf("agent system prompt = %q, want agent guidance", agent[0].Content)
	}
	if agent[1].Content != "hola" || agent[2].Content != "buenas" || agent[3].Content != followUpPrompt {
		t.Fatalf("agent messages = %#v, want prior history plus current prompt", agent)
	}
}

func TestBuildRequestMessagesUsesSystemOverride(t *testing.T) {
	m := newModel(config.Config{SystemPrompt: "sistema base"}, "")

	messages := m.buildRequestMessages(followUpPrompt, "sistema comando", "gemma4")
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
	if messages[0].Content != "sistema comando" {
		t.Fatalf("system content = %q, want command override", messages[0].Content)
	}
}

func TestBuildRequestMessagesKeepsSystemPromptInReasoningMode(t *testing.T) {
	m := newModel(config.Config{SystemPrompt: "sistema base", Model: "gemma4"}, "")
	m.mode = modeReasoning

	messages := m.buildRequestMessages(followUpPrompt, "", "gemma4")
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
	if messages[0].Content != "sistema base" {
		t.Fatalf("system content = %q, want unchanged prompt", messages[0].Content)
	}
}

func TestBuildRequestMessagesStripsLegacyThinkingTokenFromSystemPrompt(t *testing.T) {
	m := newModel(config.Config{SystemPrompt: "sistema base", Model: "qwen2.5"}, "")
	m.mode = modeReasoning

	messages := m.buildRequestMessages(followUpPrompt, "<|think|>\ncustom system", "qwen2.5")
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
	if messages[0].Content != "custom system" {
		t.Fatalf("system content = %q, want stripped token", messages[0].Content)
	}
}

func TestSlashHelpTextUsesCompactHint(t *testing.T) {
	m := newModel(config.Config{Commands: map[string]config.SlashCommand{
		"fix":     {Template: fixTemplate},
		"explain": {Template: "explain {{.Input}}"},
	}}, "")

	help := m.slashHelpText()
	if help != "" {
		t.Fatalf("slashHelpText() = %q, want empty", help)
	}
}

func TestCountTokenUsageSeparatesSystemAndContent(t *testing.T) {
	messages := []ollama.ChatMessage{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "user content"},
		{Role: "assistant", Content: "assistant history"},
	}

	usage := countTokenUsage(messages)
	if usage.system != search.ApproximateTokenCount("system prompt") {
		t.Fatalf("system tokens = %d, want system-only count", usage.system)
	}
	wantContent := search.ApproximateTokenCount("user content") + search.ApproximateTokenCount("assistant history")
	if usage.content != wantContent {
		t.Fatalf("content tokens = %d, want %d", usage.content, wantContent)
	}
	if usage.total() != usage.system+usage.content {
		t.Fatalf("total tokens = %d, want %d", usage.total(), usage.system+usage.content)
	}
}

func TestFormatTokenUsageUsesCompactCounts(t *testing.T) {
	formatted := formatTokenUsage(tokenUsage{system: 1200, content: 2300})
	if formatted != "Tokens 3.5k [ System 1.2k · Content 2.3k ]" {
		t.Fatalf("formatTokenUsage() = %q, want compact breakdown", formatted)
	}
}

func TestHandleStreamDoneAppendsSuccessfulExchangeToSession(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.pendingUserInput = pendingUserPrompt
	m.blocks = []messageBlock{{role: "assistant", raw: assistantResponse, rendered: assistantResponse}}
	m.activeBlockIndex = 0
	m.requesting = true

	m.handleStreamDone()

	if len(m.session) != 2 {
		t.Fatalf("session len = %d, want 2", len(m.session))
	}
	if m.session[0] != (ollama.ChatMessage{Role: "user", Content: pendingUserPrompt}) {
		t.Fatalf("session[0] = %#v, want stored user message", m.session[0])
	}
	if m.session[1] != (ollama.ChatMessage{Role: "assistant", Content: assistantResponse}) {
		t.Fatalf("session[1] = %#v, want stored assistant response", m.session[1])
	}
	if m.pendingUserInput != "" {
		t.Fatalf(wantEmptyPendingUserInput, m.pendingUserInput)
	}
}

func TestHandleStreamDoneAppendsSyntheticSourcesForSearchWithoutCitations(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.pendingUserInput = pendingUserPrompt
	m.pendingSearchDocs = []search.Document{{URL: testSourceURLA}, {URL: testSourceURLB}}
	m.blocks = []messageBlock{{role: "assistant", raw: "respuesta sin citas", rendered: "respuesta sin citas"}}
	m.activeBlockIndex = 0
	m.requesting = true

	m.handleStreamDone()

	if !strings.Contains(m.blocks[0].raw, sourcesFooterHeading) {
		t.Fatalf("assistant raw = %q, want synthetic sources footer", m.blocks[0].raw)
	}
	if !strings.Contains(m.blocks[0].raw, "- [1] "+testSourceURLA) {
		t.Fatalf("assistant raw = %q, want first synthetic source link", m.blocks[0].raw)
	}
	if !strings.Contains(m.session[1].Content, sourcesFooterHeading) {
		t.Fatalf("session[1] = %#v, want stored assistant response with synthetic sources", m.session[1])
	}
	if len(m.lastSearchDocs) != 2 {
		t.Fatalf("lastSearchDocs len = %d, want 2 persisted sources for navigation", len(m.lastSearchDocs))
	}
	if m.pendingSearchDocs != nil {
		t.Fatalf("pendingSearchDocs = %#v, want cleared state", m.pendingSearchDocs)
	}
}

func TestHandleStreamDoneDoesNotAppendSyntheticSourcesWhenCitationsExist(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.pendingUserInput = pendingUserPrompt
	m.pendingSearchDocs = []search.Document{{URL: testSourceURLA}}
	m.blocks = []messageBlock{{role: "assistant", raw: "respuesta con cita [1]", rendered: "respuesta con cita [1]"}}
	m.activeBlockIndex = 0
	m.requesting = true

	m.handleStreamDone()

	if strings.Count(m.blocks[0].raw, sourcesFooterHeading) != 0 {
		t.Fatalf("assistant raw = %q, want no synthetic footer when citations already exist", m.blocks[0].raw)
	}
}

func TestHandleStreamDoneStartsDeferredSemanticCachePersist(t *testing.T) {
	persisted := make(chan struct{}, 1)
	var gotQuery string
	var gotDocs []search.Document
	m := newModel(config.Config{}, "")
	m.searchBuilder = &stubSearchBuilder{
		persist: func(query string, documents []search.Document, onProgress func(search.ProgressUpdate)) <-chan struct{} {
			gotQuery = query
			gotDocs = append([]search.Document(nil), documents...)
			done := make(chan struct{})
			go func() {
				onProgress(search.ProgressUpdate{Key: search.CachePersistKey(), Kind: search.ProgressKindStep, Text: "Guardando cache", State: search.ProgressPending})
				close(done)
				persisted <- struct{}{}
			}()
			return done
		},
	}
	m.pendingUserInput = pendingUserPrompt
	m.pendingSearchDocs = []search.Document{{URL: testSourceURLA}}
	m.pendingSearchCacheQuery = "consulta"
	m.pendingSearchCacheDocs = []search.Document{{Title: "A", URL: testSourceURLA, Content: "contenido"}}
	m.blocks = []messageBlock{{role: "assistant", raw: assistantResponse, rendered: assistantResponse}}
	m.activeBlockIndex = 0
	m.requesting = true

	cmd := m.handleStreamDone()
	if cmd == nil {
		t.Fatal("handleStreamDone() returned nil cmd, want deferred cache persist command")
	}
	msg := cmd()
	progressMsg, ok := msg.(cachePersistProgressMsg)
	if !ok {
		t.Fatalf("first deferred message = %T, want cachePersistProgressMsg", msg)
	}
	if progressMsg.update.Key != search.CachePersistKey() {
		t.Fatalf("progress key = %q, want cache persist key", progressMsg.update.Key)
	}
	if gotQuery != "consulta" {
		t.Fatalf("persist query = %q, want consulta", gotQuery)
	}
	if len(gotDocs) != 1 || gotDocs[0].URL != testSourceURLA {
		t.Fatalf("persist docs = %#v, want deferred cache docs", gotDocs)
	}
	if m.pendingSearchCacheQuery != "" || m.pendingSearchCacheDocs != nil {
		t.Fatalf("pending cache payload not cleared: query=%q docs=%#v", m.pendingSearchCacheQuery, m.pendingSearchCacheDocs)
	}

	select {
	case <-persisted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for deferred semantic-cache persistence")
	}

	next := m.handleCachePersistProgress(progressMsg)
	if next == nil {
		t.Fatal("handleCachePersistProgress() returned nil cmd, want follow-up wait command")
	}
	if _, ok := next().(cachePersistDoneMsg); !ok {
		t.Fatal("expected deferred cache persist to finish after progress update")
	}
}

func TestHandleStreamErrDoesNotAppendPendingExchangeToSession(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.pendingUserInput = pendingUserPrompt
	m.requesting = true

	m.handleStreamErr(streamErrMsg{err: http.ErrHandlerTimeout})

	if len(m.session) != 0 {
		t.Fatalf("session len = %d, want 0", len(m.session))
	}
	if m.pendingUserInput != "" {
		t.Fatalf(wantEmptyPendingUserInput, m.pendingUserInput)
	}
}

func TestFormatRequestErrorDistinguishesSearchTimeout(t *testing.T) {
	m := newModel(config.Config{}, "")
	err := stageRequestErr(requestStageSearch, context.DeadlineExceeded)

	if got := m.formatRequestError(err); got != "Timeout durante la busqueda web" {
		t.Fatalf("formatRequestError() = %q, want search timeout message", got)
	}
}

func TestFormatRequestErrorDistinguishesLLMError(t *testing.T) {
	m := newModel(config.Config{}, "")
	err := stageRequestErr(requestStageLLM, errors.New("ollama status 500"))

	if got := m.formatRequestError(err); got != "Error del LLM: ollama status 500" {
		t.Fatalf("formatRequestError() = %q, want llm error message", got)
	}
}

func TestFormatRequestErrorTreatsLLMContextCancellationAsTimeout(t *testing.T) {
	m := newModel(config.Config{}, "")
	err := stageRequestErr(requestStageLLM, context.Canceled)

	if got := m.formatRequestError(err); got != "Timeout esperando respuesta del LLM" {
		t.Fatalf("formatRequestError() = %q, want llm timeout message", got)
	}
}

func TestHandleStreamErrShowsCanceledOnlyForUserCancellation(t *testing.T) {
	m := newModel(config.Config{}, "")
	m.userCanceled = true
	m.requesting = true

	m.handleStreamErr(streamErrMsg{err: context.Canceled})

	if len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].raw != "Peticion cancelada" {
		t.Fatalf("last error block = %#v, want explicit user cancellation", m.blocks)
	}
}
