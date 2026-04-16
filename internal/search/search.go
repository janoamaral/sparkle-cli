package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/cixtor/readability"
)

const (
	maxSearchResults    = 3
	maxArticleTextLen   = 4000
	maxSearchSnippetLen = 600
	approxCharsPerToken = 4
	MaxPromptTokens     = 30000
	queryOriginalLabel  = "Consulta original: "
	insufficientInfoMsg = "La informacion proporcionada es insuficiente"
)

type ProgressState string

const (
	ProgressPending ProgressState = "pending"
	ProgressDone    ProgressState = "done"
	ProgressInfo    ProgressState = "info"
)

type ProgressKind string

const (
	ProgressKindStep     ProgressKind = "step"
	ProgressKindSearch   ProgressKind = "search"
	ProgressKindDownload ProgressKind = "download"
	ProgressKindLLM      ProgressKind = "llm"
)

type ProgressUpdate struct {
	Key   string
	Kind  ProgressKind
	Text  string
	State ProgressState
}

type Document struct {
	Title   string
	URL     string
	Excerpt string
	Content string
}

type SourceSummary struct {
	Title   string
	URL     string
	Summary string
}

type PreparedPrompt struct {
	Query        string
	Prompt       string
	ApproxTokens int
	Documents    []Document
}

func (p PreparedPrompt) RequiresReduction(limit int) bool {
	if limit <= 0 {
		limit = MaxPromptTokens
	}
	return p.ApproxTokens > limit
}

func (p PreparedPrompt) BuildDocumentPrompt(document Document) string {
	return buildDocumentSummaryPrompt(p.Query, document)
}

func (p PreparedPrompt) BuildFinalPrompt(summaries []SourceSummary) string {
	return buildFinalSummaryPrompt(p.Query, summaries)
}

type articleParser func(io.Reader, string) (readability.Article, error)

type Service struct {
	searchURL string
	http      *http.Client
	parse     articleParser
}

type searxResponse struct {
	Results []Result `json:"results"`
}

type Result struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

func NewService(searchURL string) *Service {
	return &Service{
		searchURL: strings.TrimSpace(searchURL),
		http:      &http.Client{},
		parse: func(input io.Reader, pageURL string) (readability.Article, error) {
			return readability.New().Parse(input, pageURL)
		},
	}
}

func (s *Service) Prepare(ctx context.Context, query string, onActivity func(), onProgress func(ProgressUpdate)) (PreparedPrompt, error) {
	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		return PreparedPrompt{}, fmt.Errorf("slash command /search requires input")
	}
	if s == nil {
		return PreparedPrompt{}, fmt.Errorf("search service is not configured")
	}
	if strings.TrimSpace(s.searchURL) == "" {
		return PreparedPrompt{}, fmt.Errorf("search url is not configured")
	}
	notifyActivity(onActivity)
	notifyProgress(onProgress, ProgressUpdate{
		Key:   "query",
		Kind:  ProgressKindStep,
		Text:  "Consulta de búsqueda: " + trimmedQuery,
		State: ProgressDone,
	})

	results, err := s.search(ctx, trimmedQuery, onActivity, onProgress)
	if err != nil {
		return PreparedPrompt{}, err
	}
	if len(results) == 0 {
		return PreparedPrompt{}, fmt.Errorf("search returned no results")
	}

	notifyProgress(onProgress, ProgressUpdate{
		Key:   "downloads",
		Kind:  ProgressKindStep,
		Text:  fmt.Sprintf("Descargando y procesando %d fuentes", len(results)),
		State: ProgressPending,
	})

	documents, err := s.fetchDocuments(ctx, results, onActivity, onProgress)
	if err != nil {
		return PreparedPrompt{}, err
	}
	if len(documents) == 0 {
		return PreparedPrompt{}, fmt.Errorf("could not extract readable content from search results")
	}
	notifyProgress(onProgress, ProgressUpdate{
		Key:   "downloads",
		Kind:  ProgressKindStep,
		Text:  fmt.Sprintf("Fuentes procesadas: %d", len(documents)),
		State: ProgressDone,
	})

	prompt := buildSummaryPrompt(trimmedQuery, documents)
	prepared := PreparedPrompt{
		Query:        trimmedQuery,
		Prompt:       prompt,
		ApproxTokens: ApproximateTokenCount(prompt),
		Documents:    append([]Document(nil), documents...),
	}
	return prepared, nil
}

func (s *Service) search(ctx context.Context, rewrittenQuery string, onActivity func(), onProgress func(ProgressUpdate)) ([]Result, error) {
	endpoint, err := url.Parse(s.searchURL)
	if err != nil {
		return nil, fmt.Errorf("parse search url: %w", err)
	}

	params := endpoint.Query()
	params.Set("q", rewrittenQuery)
	params.Set("pageno", "1")
	params.Set("language", "auto")
	params.Set("time_range", "")
	params.Set("safesearch", "0")
	params.Set("format", "json")
	endpoint.RawQuery = params.Encode()
	notifyProgress(onProgress, ProgressUpdate{
		Key:   "search-request",
		Kind:  ProgressKindSearch,
		Text:  endpoint.String(),
		State: ProgressPending,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create search request: %w", err)
	}

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request searxng: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		message := strings.TrimSpace(string(payload))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("searxng status %d: %s", resp.StatusCode, message)
	}

	var decoded searxResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode searxng response: %w", err)
	}
	notifyActivity(onActivity)
	notifyProgress(onProgress, ProgressUpdate{
		Key:   "search-request",
		Kind:  ProgressKindSearch,
		Text:  endpoint.String(),
		State: ProgressDone,
	})

	results := selectTopResults(decoded.Results, maxSearchResults)
	return results, nil
}

func selectTopResults(results []Result, limit int) []Result {
	filtered := make([]Result, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.URL) == "" {
			continue
		}
		filtered = append(filtered, result)
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].Score > filtered[j].Score
	})

	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}

	return filtered
}

func (s *Service) fetchDocuments(ctx context.Context, results []Result, onActivity func(), onProgress func(ProgressUpdate)) ([]Document, error) {
	documents := make([]Document, 0, len(results))
	for _, result := range results {
		document, ok, err := s.fetchDocument(ctx, result, onActivity, onProgress)
		if err != nil {
			return nil, err
		}
		if ok {
			documents = append(documents, document)
		}
	}
	return documents, nil
}

func (s *Service) fetchDocument(ctx context.Context, result Result, onActivity func(), onProgress func(ProgressUpdate)) (Document, bool, error) {
	progressKey := "download:" + strings.TrimSpace(result.URL)
	notifyProgress(onProgress, ProgressUpdate{
		Key:   progressKey,
		Kind:  ProgressKindDownload,
		Text:  strings.TrimSpace(result.URL),
		State: ProgressPending,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, result.URL, nil)
	if err != nil {
		return Document{}, false, fmt.Errorf("create article request %s: %w", result.URL, err)
	}

	resp, err := s.http.Do(req)
	if err != nil {
		return Document{}, false, fmt.Errorf("request article %s: %w", result.URL, err)
	}
	defer resp.Body.Close()
	notifyActivity(onActivity)

	if resp.StatusCode != http.StatusOK {
		return fallbackDocument(result), fallbackDocument(result).Content != "", nil
	}

	article, err := s.parse(resp.Body, result.URL)
	if err != nil {
		fallback := fallbackDocument(result)
		return fallback, fallback.Content != "", nil
	}

	content := clampText(article.TextContent, maxArticleTextLen)
	if content == "" {
		fallback := fallbackDocument(result)
		return fallback, fallback.Content != "", nil
	}

	title := strings.TrimSpace(article.Title)
	if title == "" {
		title = strings.TrimSpace(result.Title)
	}

	notifyProgress(onProgress, ProgressUpdate{
		Key:   progressKey,
		Kind:  ProgressKindDownload,
		Text:  strings.TrimSpace(result.URL),
		State: ProgressDone,
	})

	return Document{
		Title:   title,
		URL:     result.URL,
		Excerpt: clampText(article.Excerpt, maxSearchSnippetLen),
		Content: content,
	}, true, nil
}

func notifyActivity(onActivity func()) {
	if onActivity != nil {
		onActivity()
	}
}

func notifyProgress(onProgress func(ProgressUpdate), update ProgressUpdate) {
	if onProgress != nil {
		onProgress(update)
	}
}

func fallbackDocument(result Result) Document {
	return Document{
		Title:   strings.TrimSpace(result.Title),
		URL:     strings.TrimSpace(result.URL),
		Excerpt: clampText(result.Content, maxSearchSnippetLen),
		Content: clampText(result.Content, maxSearchSnippetLen),
	}
}

func buildSummaryPrompt(originalQuery string, documents []Document) string {
	var builder strings.Builder
	appendEvidenceAnswerInstructions(&builder, "fuentes")
	builder.WriteString(queryOriginalLabel)
	builder.WriteString(originalQuery)
	builder.WriteString("\n\n")
	builder.WriteString("Fuentes extraídas:\n")
	for i, document := range documents {
		builder.WriteString(fmt.Sprintf("\n[%d] %s\n", i+1, safeTitle(document.Title)))
		builder.WriteString("URL: ")
		builder.WriteString(document.URL)
		builder.WriteString("\n")
		if document.Excerpt != "" {
			builder.WriteString("Extracto: ")
			builder.WriteString(document.Excerpt)
			builder.WriteString("\n")
		}
		builder.WriteString("Contenido:\n")
		builder.WriteString(document.Content)
		builder.WriteString("\n")
	}

	builder.WriteString("\nFuentes Consultadas:\n")
	for _, document := range documents {
		builder.WriteString("- ")
		builder.WriteString(safeTitle(document.Title))
		builder.WriteString(" — ")
		builder.WriteString(document.URL)
		builder.WriteString("\n")
	}

	return builder.String()
}

func buildDocumentSummaryPrompt(query string, document Document) string {
	var builder strings.Builder
	builder.WriteString("System Role:\n")
	builder.WriteString("Actua como un motor de extraccion basado estrictamente en evidencia. Tu unico proposito es destilar lo que dice esta fuente sin agregar conocimientos previos ni inferencias externas.\n\n")
	builder.WriteString("Contexto y Reglas:\n")
	builder.WriteString("- Fidelidad Absoluta: Usa solo la informacion de esta fuente. Si no alcanza para responder, escribe exactamente: \"")
	builder.WriteString(insufficientInfoMsg)
	builder.WriteString("\".\n")
	builder.WriteString("- Cobertura: Resume solo los datos utiles para responder la consulta original.\n")
	builder.WriteString("- Trazabilidad: Cada afirmacion debe poder rastrearse a esta unica fuente.\n")
	builder.WriteString("- Formato: Devuelve un resumen breve en parrafos, sin markdown adicional ni texto introductorio.\n\n")
	builder.WriteString(queryOriginalLabel)
	builder.WriteString(query)
	builder.WriteString("\n")
	builder.WriteString("Fuente: ")
	builder.WriteString(safeTitle(document.Title))
	builder.WriteString("\n")
	builder.WriteString("URL: ")
	builder.WriteString(document.URL)
	builder.WriteString("\n")
	if document.Excerpt != "" {
		builder.WriteString("Extracto: ")
		builder.WriteString(document.Excerpt)
		builder.WriteString("\n")
	}
	builder.WriteString("Contenido:\n")
	builder.WriteString(document.Content)
	return builder.String()
}

func buildFinalSummaryPrompt(query string, summaries []SourceSummary) string {
	var builder strings.Builder
	appendEvidenceAnswerInstructions(&builder, "resumenes de fuentes")
	builder.WriteString(queryOriginalLabel)
	builder.WriteString(query)
	builder.WriteString("\n\n")
	builder.WriteString("Resúmenes por fuente:\n")
	for index, summary := range summaries {
		builder.WriteString(fmt.Sprintf("\n[%d] %s\n", index+1, safeTitle(summary.Title)))
		builder.WriteString("URL: ")
		builder.WriteString(summary.URL)
		builder.WriteString("\n")
		builder.WriteString("Resumen:\n")
		builder.WriteString(strings.TrimSpace(summary.Summary))
		builder.WriteString("\n")
	}

	builder.WriteString("\nFuentes Consultadas:\n")
	for _, summary := range summaries {
		builder.WriteString("- ")
		builder.WriteString(safeTitle(summary.Title))
		builder.WriteString(" — ")
		builder.WriteString(summary.URL)
		builder.WriteString("\n")
	}

	return builder.String()
}

func appendEvidenceAnswerInstructions(builder *strings.Builder, sourceLabel string) {
	builder.WriteString("System Role:\n")
	builder.WriteString("Actua como un motor de respuesta basado estrictamente en evidencia. Tu unico proposito es destilar la verdad, o lo que digan estas ")
	builder.WriteString(sourceLabel)
	builder.WriteString(", en una respuesta concisa, estructurada y verificable. Si las fuentes dicen algo incorrecto o inesperado, debes reflejarlo tal como aparece sin corregirlo con conocimiento previo.\n\n")
	builder.WriteString("Contexto y Reglas:\n")
	builder.WriteString("- Fidelidad Absoluta: Usa solo la informacion proporcionada. Si la respuesta no esta en las fuentes, escribe exactamente: \"")
	builder.WriteString(insufficientInfoMsg)
	builder.WriteString("\".\n")
	builder.WriteString("- Citas Estrictas: Cada afirmacion debe terminar con una cita numerica como [1] o [1, 3]. No dejes afirmaciones sin cita.\n")
	builder.WriteString("- Manejo de Conflictos: Si dos fuentes se contradicen, expone la contradiccion explicitamente e indica que fuente sostiene cada version.\n")
	builder.WriteString("- Estructura: Empieza con una respuesta directa de una sola oracion. Sigue con parrafos tematicos. Termina con una seccion titulada \"Fuentes Consultadas\" que corresponda a la numeracion usada.\n")
	builder.WriteString("- Estilo: No agregues preambulos, advertencias ni conocimiento externo.\n\n")
}

func ApproximateTokenCount(value string) int {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		return 0
	}
	length := len([]rune(cleaned))
	tokens := length / approxCharsPerToken
	if length%approxCharsPerToken != 0 {
		tokens++
	}
	if tokens == 0 {
		return 1
	}
	return tokens
}

func safeTitle(value string) string {
	title := strings.TrimSpace(value)
	if title == "" {
		return "Sin título"
	}
	return title
}

func clampText(value string, limit int) string {
	cleaned := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 || len(cleaned) <= limit {
		return cleaned
	}
	if limit <= 3 {
		return cleaned[:limit]
	}
	return strings.TrimSpace(cleaned[:limit-3]) + "..."
}
