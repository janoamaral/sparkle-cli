package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/cixtor/readability"
	"github.com/pkoukk/tiktoken-go"
	"golang.org/x/sync/errgroup"
)

const (
	maxPrimarySearchResults = 5
	maxBackupSearchResults  = 3
	maxSearchResults        = maxPrimarySearchResults + maxBackupSearchResults
	maxArticleTextLen       = 4000
	maxSearchSnippetLen     = 600
	MaxPromptTokens         = 30000
	systemRoleLabel         = "System Role:\n"
	queryOriginalLabel      = "Consulta original: "
	searchQueryLabel        = "Query usada para la busqueda: "
	insufficientInfoMsg     = "La informacion proporcionada es insuficiente"
	tokenEncodingName       = tiktoken.MODEL_CL100K_BASE
	responseLanguageMsg     = "- Idioma de Respuesta: Responde en el mismo idioma dominante de la consulta original. Si la consulta esta en espanol, responde en espanol. Si esta en ingles, responde en ingles. No cambies de idioma salvo que la consulta lo pida explicitamente.\n"
)

var (
	tokenEncoderOnce  sync.Once
	tokenEncoder      *tiktoken.Tiktoken
	tokenEncoderErr   error
	videoHostSuffixes = []string{
		"youtube.com",
		"youtube-nocookie.com",
		"youtu.be",
		"vimeo.com",
		"dailymotion.com",
		"tiktok.com",
		"twitch.tv",
		"rumble.com",
		"bilibili.com",
		"loom.com",
		"wistia.com",
		"kick.com",
	}
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

type fetchedDocument struct {
	document  Document
	include   bool
	processed bool
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

func (s *Service) Prepare(ctx context.Context, query string, searchQuery string, onActivity func(), onProgress func(ProgressUpdate)) (PreparedPrompt, error) {
	var callbackMu sync.Mutex
	safeOnActivity := func() {
		if onActivity == nil {
			return
		}
		callbackMu.Lock()
		defer callbackMu.Unlock()
		onActivity()
	}
	safeOnProgress := func(update ProgressUpdate) {
		if onProgress == nil {
			return
		}
		callbackMu.Lock()
		defer callbackMu.Unlock()
		onProgress(update)
	}

	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		return PreparedPrompt{}, fmt.Errorf("slash command /search requires input")
	}
	trimmedSearchQuery := strings.TrimSpace(searchQuery)
	if trimmedSearchQuery == "" {
		trimmedSearchQuery = trimmedQuery
	}
	if s == nil {
		return PreparedPrompt{}, fmt.Errorf("search service is not configured")
	}
	if strings.TrimSpace(s.searchURL) == "" {
		return PreparedPrompt{}, fmt.Errorf("search url is not configured")
	}
	notifyActivity(safeOnActivity)
	notifyProgress(safeOnProgress, ProgressUpdate{
		Key:   "query",
		Kind:  ProgressKindStep,
		Text:  "Consulta de búsqueda: " + trimmedQuery,
		State: ProgressDone,
	})

	results, err := s.search(ctx, trimmedSearchQuery, safeOnActivity, safeOnProgress)
	if err != nil {
		return PreparedPrompt{}, err
	}
	if len(results) == 0 {
		return PreparedPrompt{}, fmt.Errorf("search returned no results")
	}

	notifyProgress(safeOnProgress, ProgressUpdate{
		Key:   "downloads",
		Kind:  ProgressKindStep,
		Text:  fmt.Sprintf("Descargando hasta %d fuentes (%d reservas)", maxPrimarySearchResults, maxBackupSearchResults),
		State: ProgressPending,
	})

	documents, processedCount, err := s.fetchDocuments(ctx, results, maxPrimarySearchResults, safeOnActivity, safeOnProgress)
	if err != nil {
		return PreparedPrompt{}, err
	}
	if len(documents) == 0 {
		return PreparedPrompt{}, fmt.Errorf("could not extract readable content from search results")
	}
	notifyProgress(safeOnProgress, ProgressUpdate{
		Key:   "downloads",
		Kind:  ProgressKindStep,
		Text:  fmt.Sprintf("Fuentes procesadas: %d", processedCount),
		State: ProgressDone,
	})

	prompt := buildSummaryPrompt(trimmedQuery, trimmedSearchQuery, documents)
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
		trimmedURL := strings.TrimSpace(result.URL)
		if trimmedURL == "" || isVideoResult(trimmedURL) {
			continue
		}
		result.URL = trimmedURL
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

func isVideoResult(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return false
	}
	for _, suffix := range videoHostSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func (s *Service) fetchDocuments(ctx context.Context, results []Result, desiredCount int, onActivity func(), onProgress func(ProgressUpdate)) ([]Document, int, error) {
	if desiredCount <= 0 {
		desiredCount = maxPrimarySearchResults
	}
	if len(results) == 0 {
		return nil, 0, nil
	}

	primaryCount := desiredCount
	if primaryCount > len(results) {
		primaryCount = len(results)
	}
	primaryResults := results[:primaryCount]
	backupResults := results[primaryCount:]

	primaryFetched, err := s.fetchBatch(ctx, primaryResults, onActivity, onProgress)
	if err != nil {
		return nil, 0, err
	}

	documents, processedCount := collectFetchedDocuments(primaryFetched, desiredCount)
	if len(documents) >= desiredCount || len(backupResults) == 0 {
		return documents, processedCount, nil
	}

	notifyProgress(onProgress, ProgressUpdate{
		Key:   "downloads-backup",
		Kind:  ProgressKindStep,
		Text:  fmt.Sprintf("Activando %d fuentes de reserva", len(backupResults)),
		State: ProgressInfo,
	})

	backupFetched, err := s.fetchBatch(ctx, backupResults, onActivity, onProgress)
	if err != nil {
		return nil, 0, err
	}

	backupDocuments, backupProcessedCount := collectFetchedDocuments(backupFetched, desiredCount-len(documents))
	documents = append(documents, backupDocuments...)
	processedCount += backupProcessedCount
	return documents, processedCount, nil
}

func (s *Service) fetchBatch(ctx context.Context, results []Result, onActivity func(), onProgress func(ProgressUpdate)) ([]fetchedDocument, error) {
	fetched := make([]fetchedDocument, len(results))
	group, groupCtx := errgroup.WithContext(ctx)

	for index, result := range results {
		index := index
		result := result
		group.Go(func() error {
			current, err := s.fetchDocument(groupCtx, result, onActivity, onProgress)
			if err != nil {
				return err
			}
			fetched[index] = current
			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return nil, err
	}

	return fetched, nil
}

func collectFetchedDocuments(fetched []fetchedDocument, limit int) ([]Document, int) {
	documents := make([]Document, 0, len(fetched))
	processedCount := 0
	for _, current := range fetched {
		if !current.processed {
			continue
		}
		processedCount++
		if limit > 0 && len(documents) >= limit {
			continue
		}
		documents = append(documents, current.document)
	}

	return documents, processedCount
}

func (s *Service) fetchDocument(ctx context.Context, result Result, onActivity func(), onProgress func(ProgressUpdate)) (fetchedDocument, error) {
	progressKey := "download:" + strings.TrimSpace(result.URL)
	notifyProgress(onProgress, ProgressUpdate{
		Key:   progressKey,
		Kind:  ProgressKindDownload,
		Text:  strings.TrimSpace(result.URL),
		State: ProgressPending,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, result.URL, nil)
	if err != nil {
		if isContextError(err) {
			return fetchedDocument{}, fmt.Errorf("create article request %s: %w", result.URL, err)
		}
		return failedFetchResult(progressKey, result.URL, onProgress), nil
	}

	resp, err := s.http.Do(req)
	if err != nil {
		if isContextError(err) {
			return fetchedDocument{}, fmt.Errorf("request article %s: %w", result.URL, err)
		}
		return failedFetchResult(progressKey, result.URL, onProgress), nil
	}
	defer resp.Body.Close()
	notifyActivity(onActivity)

	if resp.StatusCode != http.StatusOK {
		return failedFetchResult(progressKey, result.URL, onProgress), nil
	}

	article, err := s.parse(resp.Body, result.URL)
	if err != nil {
		return failedFetchResult(progressKey, result.URL, onProgress), nil
	}

	content := clampText(article.TextContent, maxArticleTextLen)
	if content == "" {
		return failedFetchResult(progressKey, result.URL, onProgress), nil
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

	return fetchedDocument{
		document: Document{
			Title:   title,
			URL:     result.URL,
			Excerpt: clampText(article.Excerpt, maxSearchSnippetLen),
			Content: content,
		},
		processed: true,
	}, nil
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

func failedFetchResult(progressKey string, sourceURL string, onProgress func(ProgressUpdate)) fetchedDocument {
	notifyProgress(onProgress, ProgressUpdate{
		Key:   progressKey,
		Kind:  ProgressKindDownload,
		Text:  strings.TrimSpace(sourceURL),
		State: ProgressInfo,
	})
	return fetchedDocument{}
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func BuildSearchRewritePrompt(query string) string {
	var builder strings.Builder
	builder.WriteString(systemRoleLabel)
	builder.WriteString("Actua como un Ingeniero de SEO Senior especializado en optimizacion de busqueda semantica. Tu mision es traducir preguntas vagas en lenguaje natural a una Query Maestra: una cadena de busqueda optimizada para maximizar la relevancia y minimizar el ruido en motores de busqueda como Google, Bing, Perplexity o SearXNG.\n\n")
	builder.WriteString("Instrucciones de Transformacion:\n")
	builder.WriteString("- Elimina stop words, cortesia, articulos innecesarios y verbos de relleno.\n")
	builder.WriteString("- Identifica las entidades, tecnologias, errores, normas o conceptos centrales.\n")
	builder.WriteString("- Expande sinonimos tecnicos, aliases, nombres antiguos y terminos equivalentes cuando mejoren la recuperacion semantica.\n")
	builder.WriteString("- Ajusta la intencion de busqueda: usa terminos como guia o tutorial para aprendizaje; fix, troubleshooting, issue, error o workaround para diagnostico; migration, upgrade o compatibility para cambios de version.\n")
	builder.WriteString("- Usa operadores avanzados solo si aumentan precision sin volver fragil la busqueda.\n")
	builder.WriteString("- Conserva el idioma dominante del usuario y de las entidades tecnicas.\n")
	builder.WriteString("- Prioriza una Query Primaria ampliamente util para web search.\n\n")
	builder.WriteString("Formato de Salida:\n")
	builder.WriteString("Devuelve solo texto plano con exactamente estas 3 lineas, sin explicaciones adicionales:\n")
	builder.WriteString("Query Primaria: <consulta optimizada principal>\n")
	builder.WriteString("Query de Larga Cola: <consulta especifica>\n")
	builder.WriteString("Busqueda Tecnica: <consulta con operadores si aplica; si no aplica, usa una variante tecnica segura sin operadores extremos>\n\n")
	builder.WriteString("Consulta del usuario:\n")
	builder.WriteString(strings.TrimSpace(query))
	return builder.String()
}

func ExtractPrimarySearchQuery(response string) string {
	trimmed := strings.TrimSpace(response)
	if trimmed == "" {
		return ""
	}

	lines := strings.Split(trimmed, "\n")
	for _, line := range lines {
		candidate := normalizeSearchQueryLine(line)
		lower := strings.ToLower(candidate)
		if !strings.HasPrefix(lower, "query primaria") {
			continue
		}
		for _, separator := range []string{":", "-", "="} {
			if index := strings.Index(candidate, separator); index >= 0 {
				return cleanSearchQueryCandidate(candidate[index+1:])
			}
		}
	}

	for _, line := range lines {
		candidate := cleanSearchQueryCandidate(line)
		if candidate != "" {
			return candidate
		}
	}

	return ""
}

func buildSummaryPrompt(originalQuery string, searchQuery string, documents []Document) string {
	var builder strings.Builder
	appendEvidenceAnswerInstructions(&builder, "fuentes")
	builder.WriteString(queryOriginalLabel)
	builder.WriteString(originalQuery)
	builder.WriteString("\n\n")
	if strings.TrimSpace(searchQuery) != "" && strings.TrimSpace(searchQuery) != strings.TrimSpace(originalQuery) {
		builder.WriteString(searchQueryLabel)
		builder.WriteString(searchQuery)
		builder.WriteString("\n\n")
	}
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
	for index, document := range documents {
		builder.WriteString(fmt.Sprintf("- [%d] ", index+1))
		builder.WriteString(document.URL)
		builder.WriteString("\n")
	}
	builder.WriteString("\nInstruccion obligatoria final:\n")
	builder.WriteString("- Usa citas [n] en cada afirmacion factual, incluyendo la oracion inicial.\n")
	builder.WriteString("- La seccion \"Fuentes Consultadas\" debe incluir solo las fuentes realmente citadas en la respuesta, con la misma numeracion.\n")
	builder.WriteString("- Antes de terminar, verifica que no exista ninguna afirmacion sin cita y que toda cita usada aparezca en la lista final.\n")

	return builder.String()
}

func normalizeSearchQueryLine(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimLeft(trimmed, "-*•0123456789. ")
	trimmed = strings.Trim(trimmed, "`*_ ")
	return strings.TrimSpace(trimmed)
}

func cleanSearchQueryCandidate(value string) string {
	trimmed := normalizeSearchQueryLine(value)
	trimmed = strings.Trim(trimmed, `"'`)
	trimmed = strings.Join(strings.Fields(trimmed), " ")
	return strings.TrimSpace(trimmed)
}

func buildDocumentSummaryPrompt(query string, document Document) string {
	var builder strings.Builder
	builder.WriteString(systemRoleLabel)
	builder.WriteString("Actua como un motor de extraccion basado estrictamente en evidencia. Tu unico proposito es extraer de esta fuente solo lo necesario para responder la consulta original, sin agregar conocimientos previos ni inferencias externas.\n\n")
	builder.WriteString("Contexto y Reglas:\n")
	builder.WriteString("- Fidelidad Absoluta: Usa solo la informacion de esta fuente. Si no alcanza para responder, escribe exactamente: \"")
	builder.WriteString(insufficientInfoMsg)
	builder.WriteString("\".\n")
	builder.WriteString("- Foco: Extrae solo los datos utiles para responder la consulta original. Descarta contexto lateral, relleno editorial y detalles que no ayuden a contestarla.\n")
	builder.WriteString("- Trazabilidad: Cada afirmacion debe poder rastrearse a esta unica fuente.\n")
	builder.WriteString(responseLanguageMsg)
	builder.WriteString("- Formato: Devuelve notas breves y densas en informacion, sin markdown adicional ni texto introductorio. No redactes una respuesta final al usuario; solo destila evidencia util.\n\n")
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
	for index, summary := range summaries {
		builder.WriteString(fmt.Sprintf("- [%d] ", index+1))
		builder.WriteString(summary.URL)
		builder.WriteString("\n")
	}
	builder.WriteString("\nInstruccion obligatoria final:\n")
	builder.WriteString("- Usa citas [n] en cada afirmacion factual, incluyendo la oracion inicial.\n")
	builder.WriteString("- La seccion \"Fuentes Consultadas\" debe incluir solo las fuentes realmente citadas en la respuesta, con la misma numeracion.\n")
	builder.WriteString("- Antes de terminar, verifica que no exista ninguna afirmacion sin cita y que toda cita usada aparezca en la lista final.\n")

	return builder.String()
}

func appendEvidenceAnswerInstructions(builder *strings.Builder, sourceLabel string) {
	// Usamos un raw string para legibilidad.
	// Menos llamadas a WriteString = menos sufrimiento para el recolector de basura.
	const promptTemplate = `### ROLE: STRICT EVIDENCE ENGINE
Actúa como un motor de extracción de datos purista. Tu única misión es resolver la consulta del usuario utilizando exclusivamente el bloque de fuentes proporcionado: %s.

### CONSTRAINTS (VIOLATION = FAILURE):
1. NO PREÁMBULOS: Prohibido usar "Según las fuentes...", "Basado en el texto...", o "Aquí tienes la respuesta". Empieza directamente con la información.
2. FIDELIDAD BINARIA: Si la respuesta no está explícitamente en las fuentes, responde únicamente: "%s". Cualquier otra explicación se considera un fallo de seguridad.
3. CERO ALUCINACIONES: Ignora tu entrenamiento previo. Si una fuente dice que el cielo es verde, el cielo es verde. No corrijas, no asumas, no interpretes.
4. ATOMICIDAD: Responde solo lo preguntado. Si preguntan "qué", no respondas "por qué" ni "cómo". Elimina cualquier contexto lateral, recomendaciones o cortesía.
5. CITAS OBLIGATORIAS: Cada oración DEBE terminar con una cita [n]. Si una oración no puede ser respaldada por una cita, bórrala.

### OUTPUT STRUCTURE:
1. [RESPUESTA DIRECTA]: Una o dos oraciones máximas que resuelvan la duda.
2. [DETALLES]: Solo si es estrictamente necesario, usa una lista breve.
3. [FUENTES CONSULTADAS]: Sección final obligatoria.
   Formato: "- [n] https://url"

### LANGUAGE:
%s

### EXECUTION:
Analiza, filtra y destruye cualquier palabra que no sea un dato factual extraído de las fuentes.`

	// Inyectamos las variables dinámicas
	prompt := fmt.Sprintf(promptTemplate, sourceLabel, insufficientInfoMsg, responseLanguageMsg)
	builder.WriteString(systemRoleLabel)
	builder.WriteString(prompt)
}

func ApproximateTokenCount(value string) int {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		return 0
	}
	encoder, err := getTokenEncoder()
	if err == nil {
		tokens := len(encoder.Encode(cleaned, nil, nil))
		if tokens > 0 {
			return tokens
		}
	}
	return fallbackApproximateTokenCount(cleaned)
}

func getTokenEncoder() (*tiktoken.Tiktoken, error) {
	tokenEncoderOnce.Do(func() {
		tokenEncoder, tokenEncoderErr = tiktoken.GetEncoding(tokenEncodingName)
	})
	return tokenEncoder, tokenEncoderErr
}

func fallbackApproximateTokenCount(cleaned string) int {
	const approxCharsPerToken = 4

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
