package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	htmlstd "html"
	"io"
	"math"
	"net/http"
	"net/url"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cixtor/readability"
	"github.com/pkoukk/tiktoken-go"
	htmlnode "golang.org/x/net/html"
	"golang.org/x/sync/errgroup"
)

const (
	maxPrimarySearchResults    = 5
	maxSearchVariants          = 3
	maxSearchResultsPerVariant = 3
	maxSingleQueryResults      = 6
	maxSearchCandidateResults  = 10
	maxArticleTextLen          = 24000
	maxSearchSnippetLen        = 600
	maxDownloadWorkers         = 4
	ChunkSize                  = 512
	ChunkOverlap               = 64
	MaxChunks                  = 8
	MaxPromptTokens            = 30000
	systemRoleLabel            = "System Role:\n"
	queryOriginalLabel         = "Consulta original: "
	searchQueryLabel           = "Query usada para la busqueda: "
	insufficientInfoMsg        = "La informacion proporcionada es insuficiente"
	tokenEncodingName          = tiktoken.MODEL_CL100K_BASE
	responseLanguageMsg        = "- Idioma de Respuesta: Responde en el mismo idioma dominante de la consulta original. Si la consulta esta en espanol, responde en espanol. Si esta en ingles, responde en ingles. No cambies de idioma salvo que la consulta lo pida explicitamente.\n"
	pendingCacheWaitTimeout    = 5 * time.Second
	cacheUnavailableMsg        = "Cache semantica no disponible; continuando con busqueda web"
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
	Score   float64
	Index   int
}

type Chunk struct {
	SourceURL string
	SourceIdx int
	Text      string
	Score     float64

	start int
	end   int
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
	CacheQuery   string
	CacheDocs    []Document
}

type SourceDocument struct {
	Document Document
	Markdown string
}

type SearchIntent string

const (
	SearchIntentUnknown  SearchIntent = "unknown"
	SearchIntentStatic   SearchIntent = "static"
	SearchIntentTemporal SearchIntent = "temporal"
)

type SearchPlan struct {
	OriginalQuery string
	PrimaryQuery  string
	Variants      []string
	Intent        SearchIntent
}

type SearchQueryResolver func(context.Context, string) (SearchPlan, error)

func (p SearchPlan) Queries() []string {
	queries := make([]string, 0, maxSearchVariants)
	appendQuery := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		for _, existing := range queries {
			if strings.EqualFold(existing, trimmed) {
				return
			}
		}
		queries = append(queries, trimmed)
	}

	appendQuery(p.PrimaryQuery)
	for _, variant := range p.Variants {
		appendQuery(variant)
	}
	if len(queries) == 0 {
		appendQuery(p.OriginalQuery)
	}
	if len(queries) > maxSearchVariants {
		queries = queries[:maxSearchVariants]
	}
	return queries
}

func (p SearchPlan) EffectiveQuery() string {
	queries := p.Queries()
	if len(queries) == 0 {
		return strings.TrimSpace(p.OriginalQuery)
	}
	return queries[0]
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
	searchURL      string
	http           *http.Client
	parse          articleParser
	embedder       EmbeddingProvider
	embeddingModel string
	cache          semanticCacheStore
	background     sync.WaitGroup
	pendingCacheMu sync.Mutex
	pendingCache   map[string]chan struct{}
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

type labeledSearchQuery struct {
	label string
	value string
}

type fetchedDocument struct {
	document  Document
	sourceMD  string
	processed bool
}

func NewService(searchURL string, options ...Option) *Service {
	service := &Service{
		searchURL: strings.TrimSpace(searchURL),
		http:      &http.Client{},
		parse: func(input io.Reader, pageURL string) (readability.Article, error) {
			return readability.New().Parse(input, pageURL)
		},
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

func (s *Service) Prepare(ctx context.Context, query string, searchQuery string, resolveSearchQuery SearchQueryResolver, onActivity func(), onProgress func(ProgressUpdate)) (PreparedPrompt, error) {
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
	cacheSearchQuery := trimmedSearchQuery
	if cacheSearchQuery == "" {
		cacheSearchQuery = trimmedQuery
	}
	if s == nil {
		return PreparedPrompt{}, fmt.Errorf("search service is not configured")
	}
	if strings.TrimSpace(s.searchURL) == "" {
		return PreparedPrompt{}, fmt.Errorf("search url is not configured")
	}
	if cached, ok := s.lookupCache(ctx, trimmedQuery, cacheSearchQuery, safeOnActivity, safeOnProgress); ok {
		return cached, nil
	}
	searchPlan, err := resolvePreparedSearchPlan(ctx, trimmedQuery, trimmedSearchQuery, resolveSearchQuery)
	if err != nil {
		return PreparedPrompt{}, err
	}
	notifyActivity(safeOnActivity)
	notifyProgress(safeOnProgress, ProgressUpdate{
		Key:   "query",
		Kind:  ProgressKindStep,
		Text:  "Consulta de búsqueda: " + searchPlan.EffectiveQuery(),
		State: ProgressDone,
	})

	results, err := s.search(ctx, searchPlan, safeOnActivity, safeOnProgress)
	if err != nil {
		return PreparedPrompt{}, err
	}
	if len(results) == 0 {
		return PreparedPrompt{}, fmt.Errorf("search returned no results")
	}
	desiredSourceCount := desiredSourceCountForPlan(searchPlan)

	notifySearchCandidates(safeOnProgress, results)
	notifyProgress(safeOnProgress, ProgressUpdate{
		Key:   "downloads",
		Kind:  ProgressKindStep,
		Text:  fmt.Sprintf("Descargando hasta %d candidatos para seleccionar %d fuentes [%s]", len(results), desiredSourceCount, strings.Join(searchPlan.Queries(), ", ")),
		State: ProgressPending,
	})

	documents, processedCount, err := s.fetchDocuments(ctx, results, desiredSourceCount, safeOnActivity, safeOnProgress)
	if err != nil {
		return PreparedPrompt{}, err
	}
	if len(documents) == 0 {
		return PreparedPrompt{}, fmt.Errorf("could not extract readable content from search results")
	}
	rawDocuments := append([]Document(nil), documents...)
	notifyProgress(safeOnProgress, ProgressUpdate{
		Key:   "chunk-selection",
		Kind:  ProgressKindStep,
		Text:  fmt.Sprintf("Seleccionando hasta %d fragmentos relevantes", MaxChunks),
		State: ProgressPending,
	})
	documents, selectedChunks := selectRelevantDocumentChunks(trimmedQuery, documents)
	notifyProgress(safeOnProgress, ProgressUpdate{
		Key:   "chunk-selection",
		Kind:  ProgressKindStep,
		Text:  fmt.Sprintf("Fragmentos relevantes seleccionados: %d", selectedChunks),
		State: ProgressDone,
	})
	notifyProgress(safeOnProgress, ProgressUpdate{
		Key:   "downloads",
		Kind:  ProgressKindStep,
		Text:  fmt.Sprintf("Fuentes procesadas: %d", processedCount),
		State: ProgressDone,
	})
	prepared := buildPreparedPrompt(trimmedQuery, searchPlan.EffectiveQuery(), documents)
	prepared.CacheQuery = trimmedQuery
	prepared.CacheDocs = append([]Document(nil), rawDocuments...)
	return prepared, nil
}

func resolvePreparedSearchPlan(ctx context.Context, query string, searchQuery string, resolveSearchQuery SearchQueryResolver) (SearchPlan, error) {
	trimmedSearchQuery := strings.TrimSpace(searchQuery)
	if trimmedSearchQuery != "" {
		return SearchPlan{OriginalQuery: query, PrimaryQuery: trimmedSearchQuery, Variants: []string{trimmedSearchQuery}, Intent: classifySearchIntent(query)}, nil
	}
	if resolveSearchQuery == nil {
		return SearchPlan{OriginalQuery: query, PrimaryQuery: query, Variants: []string{query}, Intent: classifySearchIntent(query)}, nil
	}
	resolvedPlan, err := resolveSearchQuery(ctx, query)
	if err != nil {
		return SearchPlan{}, err
	}
	resolvedPlan.OriginalQuery = strings.TrimSpace(query)
	if resolvedPlan.Intent == SearchIntentUnknown {
		resolvedPlan.Intent = classifySearchIntent(query)
	}
	if len(resolvedPlan.Queries()) == 0 {
		resolvedPlan.PrimaryQuery = query
		resolvedPlan.Variants = []string{query}
	}
	resolvedPlan.PrimaryQuery = resolvedPlan.EffectiveQuery()
	resolvedPlan.Variants = resolvedPlan.Queries()
	return resolvedPlan, nil
}

func (s *Service) Close() error {
	s.background.Wait()
	if s.cache != nil {
		return s.cache.Close()
	}
	return nil
}

func (s *Service) lookupCache(ctx context.Context, query string, searchQuery string, onActivity func(), onProgress func(ProgressUpdate)) (PreparedPrompt, bool) {
	if s == nil || s.cache == nil || s.embedder == nil {
		return PreparedPrompt{}, false
	}
	cacheQueryKey := normalizePendingCacheKey(query)
	notifyProgress(onProgress, ProgressUpdate{
		Key:   cacheLookupKey,
		Kind:  ProgressKindStep,
		Text:  "Consultando cache semantica en Qdrant",
		State: ProgressPending,
	})
	vectors, err := s.embedder.EmbedWithModel(ctx, s.embeddingModel, []string{query})
	if err != nil || len(vectors) == 0 || len(vectors[0]) == 0 {
		notifyProgress(onProgress, ProgressUpdate{Key: cacheLookupKey, Kind: ProgressKindStep, Text: cacheUnavailableMsg, State: ProgressInfo})
		return PreparedPrompt{}, false
	}
	hits, err := s.cache.Lookup(ctx, vectors[0], time.Now().UTC())
	if err != nil {
		notifyProgress(onProgress, ProgressUpdate{Key: cacheLookupKey, Kind: ProgressKindStep, Text: cacheUnavailableMsg, State: ProgressInfo})
		return PreparedPrompt{}, false
	}
	documents := rerankCachedChunks(query, hits)
	if len(documents) == 0 {
		if waited := s.waitForPendingCache(ctx, cacheQueryKey, onProgress); waited {
			hits, err = s.cache.Lookup(ctx, vectors[0], time.Now().UTC())
			if err != nil {
				notifyProgress(onProgress, ProgressUpdate{Key: cacheLookupKey, Kind: ProgressKindStep, Text: cacheUnavailableMsg, State: ProgressInfo})
				return PreparedPrompt{}, false
			}
			documents = rerankCachedChunks(query, hits)
		}
	}
	if len(documents) == 0 {
		notifyProgress(onProgress, ProgressUpdate{Key: cacheLookupKey, Kind: ProgressKindStep, Text: describeCacheLookup(documents), State: ProgressInfo})
		return PreparedPrompt{}, false
	}
	notifyActivity(onActivity)
	documents, selectedChunks := selectRelevantDocumentChunks(query, documents)
	notifyProgress(onProgress, ProgressUpdate{Key: cacheLookupKey, Kind: ProgressKindStep, Text: fmt.Sprintf("Cache semantica reutilizada: %d fragmentos (%d seleccionados)", len(hits), selectedChunks), State: ProgressDone})
	return buildPreparedPrompt(query, searchQuery, documents), true
}

func (s *Service) PersistSemanticCache(query string, documents []Document, onProgress func(ProgressUpdate)) <-chan struct{} {
	doneCh := make(chan struct{})
	if s == nil || s.cache == nil || s.embedder == nil || len(documents) == 0 {
		close(doneCh)
		return doneCh
	}
	cloned := append([]Document(nil), documents...)
	notifyProgress(onProgress, ProgressUpdate{
		Key:   cachePersistKey,
		Kind:  ProgressKindStep,
		Text:  fmt.Sprintf("Guardando %d fuentes en cache semantica", len(cloned)),
		State: ProgressPending,
	})
	cacheQueryKey := normalizePendingCacheKey(query)
	done := s.beginPendingCache(cacheQueryKey)
	s.background.Add(1)
	go func() {
		defer s.background.Done()
		defer close(doneCh)
		defer s.finishPendingCache(cacheQueryKey, done)
		ctx, cancel := context.WithTimeout(context.Background(), cacheIngestTimeout)
		defer cancel()
		seeds := buildCachePointSeeds(time.Now().UTC(), cloned)
		if len(seeds) == 0 {
			notifyProgress(onProgress, ProgressUpdate{Key: cachePersistKey, Kind: ProgressKindStep, Text: "No se generaron fragmentos para persistir en cache semantica", State: ProgressInfo})
			return
		}
		inputs := make([]string, 0, len(seeds))
		for _, current := range seeds {
			inputs = append(inputs, current.Content)
		}
		vectors, err := s.embedder.EmbedWithModel(ctx, s.embeddingModel, inputs)
		if err != nil {
			notifyProgress(onProgress, ProgressUpdate{Key: cachePersistKey, Kind: ProgressKindStep, Text: "No se pudo vectorizar el contenido para Qdrant", State: ProgressInfo})
			return
		}
		points := buildCachePoints(query, vectors, seeds)
		if len(points) == 0 {
			notifyProgress(onProgress, ProgressUpdate{Key: cachePersistKey, Kind: ProgressKindStep, Text: "No se generaron puntos validos para Qdrant", State: ProgressInfo})
			return
		}
		if err := s.cache.Ingest(ctx, points); err != nil {
			notifyProgress(onProgress, ProgressUpdate{Key: cachePersistKey, Kind: ProgressKindStep, Text: fmt.Sprintf("No se pudo persistir la cache semantica en Qdrant: %v", err), State: ProgressInfo})
			return
		}
		notifyProgress(onProgress, ProgressUpdate{Key: cachePersistKey, Kind: ProgressKindStep, Text: fmt.Sprintf("Cache semantica actualizada en Qdrant con %d fragmentos", len(points)), State: ProgressDone})
	}()
	return doneCh
}

func (s *Service) FetchSource(ctx context.Context, sourceURL string, title string, onActivity func(), onProgress func(ProgressUpdate)) (SourceDocument, error) {
	trimmedURL := strings.TrimSpace(sourceURL)
	if trimmedURL == "" {
		return SourceDocument{}, fmt.Errorf("source url is empty")
	}
	if s == nil {
		return SourceDocument{}, fmt.Errorf("search service is not configured")
	}

	fetched, err := s.fetchDocument(ctx, Result{Title: strings.TrimSpace(title), URL: trimmedURL}, onActivity, onProgress)
	if err != nil {
		return SourceDocument{}, err
	}
	if !fetched.processed {
		return SourceDocument{}, fmt.Errorf("could not extract readable content from source")
	}

	document := fetched.document
	return SourceDocument{
		Document: document,
		Markdown: buildSourceMarkdown(document, fetched.sourceMD),
	}, nil
}

func buildSourceMarkdown(document Document, bodyMarkdown string) string {
	title := strings.TrimSpace(document.Title)
	if title == "" {
		title = strings.TrimSpace(document.URL)
	}

	sections := make([]string, 0, 4)
	if title != "" {
		sections = append(sections, "# "+title)
	}
	if url := strings.TrimSpace(document.URL); url != "" {
		sections = append(sections, "Fuente: "+url)
	}
	if excerpt := strings.TrimSpace(document.Excerpt); excerpt != "" {
		sections = append(sections, "> "+excerpt)
	}
	content := strings.TrimSpace(bodyMarkdown)
	if content == "" {
		content = markdownizePlainText(document.Content)
	}
	if content != "" {
		sections = append(sections, content)
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func markdownizeSourceContent(htmlContent string, plainText string) string {
	markdown := strings.TrimSpace(htmlContentToMarkdown(htmlContent))
	if markdown != "" {
		return markdown
	}
	return markdownizePlainText(plainText)
}

func htmlContentToMarkdown(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}

	root, err := parseHTMLMarkdownRoot(trimmed)
	if err != nil || root == nil {
		return ""
	}
	blocks := renderHTMLNodesAsMarkdownBlocks(root.FirstChild)
	return strings.TrimSpace(strings.Join(blocks, "\n\n"))
}

func parseHTMLMarkdownRoot(content string) (*htmlnode.Node, error) {
	doc, err := htmlnode.Parse(strings.NewReader("<div>" + content + "</div>"))
	if err != nil {
		return nil, err
	}
	return findElementNode(doc, "div"), nil
}

func findElementNode(node *htmlnode.Node, tag string) *htmlnode.Node {
	if node == nil {
		return nil
	}
	if node.Type == htmlnode.ElementNode && strings.EqualFold(node.Data, tag) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findElementNode(child, tag); found != nil {
			return found
		}
	}
	return nil
}

func renderHTMLNodesAsMarkdownBlocks(node *htmlnode.Node) []string {
	blocks := make([]string, 0, 8)
	inlineParts := make([]string, 0, 4)
	flushInline := func() {
		inline := tidyInlineMarkdown(strings.Join(inlineParts, ""))
		if inline != "" {
			blocks = append(blocks, inline)
		}
		inlineParts = inlineParts[:0]
	}

	for current := node; current != nil; current = current.NextSibling {
		if shouldRenderAsMarkdownBlock(current) {
			flushInline()
			blocks = append(blocks, renderMarkdownBlocksForNode(current)...)
			continue
		}
		inline := renderInlineMarkdown(current)
		if strings.TrimSpace(inline) == "" {
			continue
		}
		inlineParts = append(inlineParts, inline)
	}

	flushInline()
	return compactMarkdownBlocks(blocks)
}

func shouldRenderAsMarkdownBlock(node *htmlnode.Node) bool {
	if node == nil {
		return false
	}
	if node.Type == htmlnode.TextNode {
		return false
	}
	if node.Type != htmlnode.ElementNode {
		return false
	}
	switch strings.ToLower(node.Data) {
	case "article", "aside", "blockquote", "body", "div", "figure", "figcaption", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "li", "main", "nav", "ol", "p", "pre", "section", "table", "tbody", "thead", "tr", "ul":
		return true
	default:
		return hasMarkdownBlockChild(node)
	}
}

func hasMarkdownBlockChild(node *htmlnode.Node) bool {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if shouldRenderAsMarkdownBlock(child) {
			return true
		}
	}
	return false
}

func renderMarkdownBlocksForNode(node *htmlnode.Node) []string {
	if node == nil {
		return nil
	}
	if node.Type == htmlnode.TextNode {
		return renderInlineTextBlock(renderInlineMarkdown(node))
	}
	if node.Type != htmlnode.ElementNode {
		return renderHTMLNodesAsMarkdownBlocks(node.FirstChild)
	}
	return renderMarkdownBlocksForElement(node)
}

func renderMarkdownBlocksForElement(node *htmlnode.Node) []string {
	switch strings.ToLower(node.Data) {
	case "article", "aside", "body", "div", "figure", "figcaption", "footer", "header", "main", "nav", "section":
		return renderHTMLNodesAsMarkdownBlocks(node.FirstChild)
	case "p":
		return renderInlineTextBlock(renderInlineMarkdownChildren(node))
	case "h1", "h2", "h3", "h4", "h5", "h6":
		text := tidyInlineMarkdown(renderInlineMarkdownChildren(node))
		if text == "" {
			return nil
		}
		level := int(node.Data[1] - '0')
		return []string{strings.Repeat("#", level) + " " + text}
	case "blockquote":
		return renderBlockquoteMarkdown(node)
	case "pre":
		code := strings.TrimSpace(extractPreformattedText(node))
		if code == "" {
			return nil
		}
		language := detectCodeFenceLanguage(node)
		return []string{"```" + language + "\n" + code + "\n```"}
	case "ul":
		list := renderListMarkdown(node, false)
		if list == "" {
			return nil
		}
		return []string{list}
	case "ol":
		list := renderListMarkdown(node, true)
		if list == "" {
			return nil
		}
		return []string{list}
	case "li":
		return renderInlineTextBlock(renderInlineMarkdownChildren(node))
	case "hr":
		return []string{"---"}
	case "table", "thead", "tbody", "tr":
		table := renderTableMarkdown(node)
		if table == "" {
			return renderHTMLNodesAsMarkdownBlocks(node.FirstChild)
		}
		return []string{table}
	default:
		if hasMarkdownBlockChild(node) {
			return renderHTMLNodesAsMarkdownBlocks(node.FirstChild)
		}
		return renderInlineTextBlock(renderInlineMarkdownChildren(node))
	}
}

func renderInlineTextBlock(value string) []string {
	text := tidyInlineMarkdown(value)
	if text == "" {
		return nil
	}
	return []string{text}
}

func renderBlockquoteMarkdown(node *htmlnode.Node) []string {
	blocks := renderHTMLNodesAsMarkdownBlocks(node.FirstChild)
	if len(blocks) == 0 {
		inline := tidyInlineMarkdown(renderInlineMarkdownChildren(node))
		if inline == "" {
			return nil
		}
		blocks = []string{inline}
	}
	prefixed := make([]string, 0, len(blocks))
	for _, block := range blocks {
		current := prefixMarkdownLines(block, "> ")
		if current != "" {
			prefixed = append(prefixed, current)
		}
	}
	return prefixed
}

func renderListMarkdown(node *htmlnode.Node, ordered bool) string {
	items := make([]string, 0, 4)
	index := 1
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != htmlnode.ElementNode || !strings.EqualFold(child.Data, "li") {
			continue
		}
		body := renderListItemMarkdown(child)
		if body == "" {
			continue
		}
		prefix := "- "
		if ordered {
			prefix = fmt.Sprintf("%d. ", index)
		}
		items = append(items, indentMarkdownListItem(body, prefix))
		index++
	}
	return strings.Join(items, "\n")
}

func renderListItemMarkdown(node *htmlnode.Node) string {
	blocks := renderHTMLNodesAsMarkdownBlocks(node.FirstChild)
	if len(blocks) == 0 {
		return tidyInlineMarkdown(renderInlineMarkdownChildren(node))
	}
	return strings.Join(blocks, "\n")
}

func indentMarkdownListItem(body string, prefix string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return ""
	}
	indent := strings.Repeat(" ", len(prefix))
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "" {
			continue
		}
		lines[index] = indent + lines[index]
	}
	if len(lines) == 1 {
		return prefix + lines[0]
	}
	return prefix + lines[0] + "\n" + strings.Join(lines[1:], "\n")
}

func renderTableMarkdown(node *htmlnode.Node) string {
	rows := collectTableRows(node)
	if len(rows) == 0 {
		return ""
	}
	width := 0
	for _, row := range rows {
		if len(row) > width {
			width = len(row)
		}
	}
	if width == 0 {
		return ""
	}
	for index := range rows {
		for len(rows[index]) < width {
			rows[index] = append(rows[index], "")
		}
	}

	lines := []string{"| " + strings.Join(rows[0], " | ") + " |"}
	separator := make([]string, width)
	for index := range separator {
		separator[index] = "---"
	}
	lines = append(lines, "| "+strings.Join(separator, " | ")+" |")
	for _, row := range rows[1:] {
		lines = append(lines, "| "+strings.Join(row, " | ")+" |")
	}
	return strings.Join(lines, "\n")
}

func collectTableRows(node *htmlnode.Node) [][]string {
	rows := make([][]string, 0, 4)
	walkHTMLNodes(node, func(current *htmlnode.Node) {
		if !isTableRowNode(current) {
			return
		}
		if cells := collectTableCells(current); len(cells) > 0 {
			rows = append(rows, cells)
		}
	})
	return rows
}

func walkHTMLNodes(node *htmlnode.Node, visit func(*htmlnode.Node)) {
	if node == nil {
		return
	}
	visit(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walkHTMLNodes(child, visit)
	}
}

func isTableRowNode(node *htmlnode.Node) bool {
	return node != nil && node.Type == htmlnode.ElementNode && strings.EqualFold(node.Data, "tr")
}

func collectTableCells(node *htmlnode.Node) []string {
	cells := make([]string, 0, 4)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if !isTableCellNode(child) {
			continue
		}
		cells = append(cells, tidyInlineMarkdown(renderInlineMarkdownChildren(child)))
	}
	return cells
}

func isTableCellNode(node *htmlnode.Node) bool {
	if node == nil || node.Type != htmlnode.ElementNode {
		return false
	}
	tag := strings.ToLower(node.Data)
	return tag == "td" || tag == "th"
}

func renderInlineMarkdownChildren(node *htmlnode.Node) string {
	var builder strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		builder.WriteString(renderInlineMarkdown(child))
	}
	return builder.String()
}

func renderInlineMarkdown(node *htmlnode.Node) string {
	if node == nil {
		return ""
	}
	switch node.Type {
	case htmlnode.TextNode:
		return normalizeInlineWhitespace(node.Data)
	case htmlnode.ElementNode:
		return renderInlineElementMarkdown(node)
	default:
		return ""
	}
}

func renderInlineElementMarkdown(node *htmlnode.Node) string {
	switch strings.ToLower(node.Data) {
	case "br":
		return "\n"
	case "strong", "b":
		return wrapMarkdownInline("**", tidyInlineMarkdown(renderInlineMarkdownChildren(node)))
	case "em", "i":
		return wrapMarkdownInline("*", tidyInlineMarkdown(renderInlineMarkdownChildren(node)))
	case "code":
		return renderInlineCodeMarkdown(node)
	case "a":
		return renderInlineLinkMarkdown(node)
	case "img":
		return strings.TrimSpace(markdownAttribute(node, "alt"))
	default:
		return renderInlineMarkdownChildren(node)
	}
}

func renderInlineCodeMarkdown(node *htmlnode.Node) string {
	if node.Parent != nil && strings.EqualFold(node.Parent.Data, "pre") {
		return ""
	}
	code := strings.TrimSpace(extractPreformattedText(node))
	if code == "" {
		code = strings.TrimSpace(tidyInlineMarkdown(renderInlineMarkdownChildren(node)))
	}
	if code == "" {
		return ""
	}
	return "`" + code + "`"
}

func renderInlineLinkMarkdown(node *htmlnode.Node) string {
	text := tidyInlineMarkdown(renderInlineMarkdownChildren(node))
	href := strings.TrimSpace(markdownAttribute(node, "href"))
	if href == "" {
		return text
	}
	if text == "" || text == href {
		return href
	}
	return fmt.Sprintf("[%s](%s)", text, href)
}

func markdownAttribute(node *htmlnode.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return htmlstd.UnescapeString(attr.Val)
		}
	}
	return ""
}

func extractPreformattedText(node *htmlnode.Node) string {
	var builder strings.Builder
	var walk func(*htmlnode.Node)
	walk = func(current *htmlnode.Node) {
		if current == nil {
			return
		}
		switch current.Type {
		case htmlnode.TextNode:
			builder.WriteString(htmlstd.UnescapeString(current.Data))
		case htmlnode.ElementNode:
			if strings.EqualFold(current.Data, "br") {
				builder.WriteString("\n")
			}
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
		}
	}
	walk(node)
	return builder.String()
}

func detectCodeFenceLanguage(node *htmlnode.Node) string {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if !isCodeNode(child) {
			continue
		}
		if language := classLanguage(child); language != "" {
			return language
		}
	}
	return ""
}

func isCodeNode(node *htmlnode.Node) bool {
	return node != nil && node.Type == htmlnode.ElementNode && strings.EqualFold(node.Data, "code")
}

func classLanguage(node *htmlnode.Node) string {
	for _, attr := range node.Attr {
		if !strings.EqualFold(attr.Key, "class") {
			continue
		}
		for _, token := range strings.Fields(attr.Val) {
			if strings.HasPrefix(token, "language-") {
				return strings.TrimPrefix(token, "language-")
			}
		}
	}
	return ""
}

func normalizeInlineWhitespace(value string) string {
	decoded := htmlstd.UnescapeString(value)
	if decoded == "" {
		return ""
	}
	runes := []rune(decoded)
	leading := len(runes) > 0 && unicode.IsSpace(runes[0])
	trailing := len(runes) > 0 && unicode.IsSpace(runes[len(runes)-1])
	fields := strings.Fields(decoded)
	if len(fields) == 0 {
		if leading || trailing {
			return " "
		}
		return ""
	}
	normalized := strings.Join(fields, " ")
	if leading {
		normalized = " " + normalized
	}
	if trailing {
		normalized += " "
	}
	return normalized
}

func tidyInlineMarkdown(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	for index, line := range lines {
		lines[index] = strings.Join(strings.Fields(line), " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func wrapMarkdownInline(marker string, value string) string {
	if value == "" {
		return ""
	}
	return marker + value + marker
}

func prefixMarkdownLines(value string, prefix string) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[index] = strings.TrimSpace(prefix)
			continue
		}
		lines[index] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func compactMarkdownBlocks(blocks []string) []string {
	compact := make([]string, 0, len(blocks))
	for _, block := range blocks {
		trimmed := strings.TrimSpace(block)
		if trimmed == "" {
			continue
		}
		compact = append(compact, trimmed)
	}
	return compact
}

func markdownizePlainText(content string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	trimmed := strings.TrimSpace(normalized)
	if trimmed == "" {
		return ""
	}

	normalizer := newSourceMarkdownNormalizer(len(strings.Split(trimmed, "\n")))
	for _, rawLine := range strings.Split(trimmed, "\n") {
		normalizer.processLine(rawLine)
	}
	return normalizer.finish()
}

type sourceMarkdownNormalizer struct {
	blocks        []string
	paragraph     []string
	markdownGroup []string
	markdownKind  string
	inFence       bool
}

func newSourceMarkdownNormalizer(capacity int) *sourceMarkdownNormalizer {
	return &sourceMarkdownNormalizer{
		blocks:        make([]string, 0, capacity),
		paragraph:     make([]string, 0, 4),
		markdownGroup: make([]string, 0, 4),
	}
}

func (n *sourceMarkdownNormalizer) processLine(rawLine string) {
	line := strings.TrimRight(rawLine, " \t")
	trimmedLine := strings.TrimSpace(line)

	if n.handleFenceContent(line, trimmedLine) {
		return
	}
	if trimmedLine == "" {
		n.flushParagraph()
		n.flushMarkdownGroup()
		return
	}
	if n.handleFenceStart(trimmedLine) {
		return
	}
	if n.handleMarkdownLine(line, trimmedLine) {
		return
	}
	n.handlePlainLine(trimmedLine)
}

func (n *sourceMarkdownNormalizer) handleFenceContent(line string, trimmedLine string) bool {
	if !n.inFence {
		return false
	}
	n.markdownGroup = append(n.markdownGroup, line)
	if isMarkdownFence(trimmedLine) {
		n.inFence = false
		n.flushMarkdownGroup()
	}
	return true
}

func (n *sourceMarkdownNormalizer) handleFenceStart(trimmedLine string) bool {
	if !isMarkdownFence(trimmedLine) {
		return false
	}
	n.flushParagraph()
	n.flushMarkdownGroup()
	n.markdownGroup = append(n.markdownGroup, trimmedLine)
	n.markdownKind = "fence"
	n.inFence = true
	return true
}

func (n *sourceMarkdownNormalizer) handleMarkdownLine(line string, trimmedLine string) bool {
	kind := markdownLineKind(line, trimmedLine)
	if kind == "" {
		return false
	}

	n.flushParagraph()
	if n.markdownKind != "" && n.markdownKind != kind {
		n.flushMarkdownGroup()
	}
	n.markdownKind = kind
	if kind == "heading" || kind == "rule" {
		n.flushMarkdownGroup()
		n.blocks = append(n.blocks, trimmedLine)
		return true
	}
	n.markdownGroup = append(n.markdownGroup, preserveMarkdownLine(kind, line, trimmedLine))
	return true
}

func (n *sourceMarkdownNormalizer) handlePlainLine(trimmedLine string) {
	n.flushMarkdownGroup()
	if len(n.paragraph) == 0 {
		n.paragraph = append(n.paragraph, trimmedLine)
		return
	}
	if shouldContinueParagraph(n.paragraph[len(n.paragraph)-1], trimmedLine) {
		n.paragraph = append(n.paragraph, trimmedLine)
		return
	}
	n.flushParagraph()
	n.paragraph = append(n.paragraph, trimmedLine)
}

func (n *sourceMarkdownNormalizer) flushParagraph() {
	if len(n.paragraph) == 0 {
		return
	}
	n.blocks = append(n.blocks, strings.Join(n.paragraph, " "))
	n.paragraph = n.paragraph[:0]
}

func (n *sourceMarkdownNormalizer) flushMarkdownGroup() {
	if len(n.markdownGroup) == 0 {
		return
	}
	n.blocks = append(n.blocks, strings.Join(n.markdownGroup, "\n"))
	n.markdownGroup = n.markdownGroup[:0]
	n.markdownKind = ""
}

func (n *sourceMarkdownNormalizer) finish() string {
	n.flushParagraph()
	n.flushMarkdownGroup()
	return strings.Join(n.blocks, "\n\n")
}

func markdownLineKind(line string, trimmed string) string {
	if trimmed == "" {
		return ""
	}
	if isMarkdownHeading(trimmed) {
		return "heading"
	}
	if isMarkdownRule(trimmed) {
		return "rule"
	}
	if isMarkdownListItem(trimmed) {
		return "list"
	}
	if strings.HasPrefix(trimmed, ">") {
		return "quote"
	}
	if isIndentedCodeLine(line) {
		return "code"
	}
	if strings.Contains(trimmed, "|") {
		return "table"
	}
	return ""
}

func preserveMarkdownLine(kind string, line string, trimmed string) string {
	switch kind {
	case "code", "table":
		return line
	default:
		return trimmed
	}
}

func isMarkdownFence(line string) bool {
	return strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~")
}

func isMarkdownHeading(line string) bool {
	if !strings.HasPrefix(line, "#") {
		return false
	}
	count := 0
	for count < len(line) && line[count] == '#' {
		count++
	}
	return count > 0 && count <= 6 && len(line) > count && line[count] == ' '
}

func isMarkdownRule(line string) bool {
	if len(line) < 3 {
		return false
	}
	compact := strings.ReplaceAll(line, " ", "")
	if len(compact) < 3 {
		return false
	}
	marker := compact[0]
	if marker != '-' && marker != '*' && marker != '_' {
		return false
	}
	for index := 1; index < len(compact); index++ {
		if compact[index] != marker {
			return false
		}
	}
	return true
}

func isMarkdownListItem(line string) bool {
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ") {
		return true
	}
	index := 0
	for index < len(line) && line[index] >= '0' && line[index] <= '9' {
		index++
	}
	return index > 0 && index+1 < len(line) && line[index] == '.' && line[index+1] == ' '
}

func isIndentedCodeLine(line string) bool {
	return strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")
}

func shouldContinueParagraph(previous string, current string) bool {
	trimmedPrevious := strings.TrimSpace(previous)
	trimmedCurrent := strings.TrimSpace(current)
	if trimmedPrevious == "" || trimmedCurrent == "" {
		return false
	}

	lastRune, ok := lastNonSpaceRune(trimmedPrevious)
	if !ok {
		return false
	}
	firstRune, ok := firstNonSpaceRune(trimmedCurrent)
	if !ok {
		return false
	}

	if strings.HasSuffix(trimmedPrevious, "-") || strings.HasSuffix(trimmedPrevious, "/") {
		return true
	}

	if strings.ContainsRune(".!?:;", lastRune) {
		return false
	}

	if len(trimmedPrevious) < 55 && (unicode.IsUpper(firstRune) || unicode.IsDigit(firstRune)) {
		return false
	}

	return true
}

func firstNonSpaceRune(value string) (rune, bool) {
	for _, current := range value {
		if !unicode.IsSpace(current) {
			return current, true
		}
	}
	return 0, false
}

func lastNonSpaceRune(value string) (rune, bool) {
	for index := len(value); index > 0; {
		current, size := utf8.DecodeLastRuneInString(value[:index])
		index -= size
		if !unicode.IsSpace(current) {
			return current, true
		}
	}
	return 0, false
}

func normalizePendingCacheKey(query string) string {
	trimmed := strings.TrimSpace(strings.ToLower(query))
	if trimmed == "" {
		return ""
	}
	return strings.Join(strings.Fields(trimmed), " ")
}

func (s *Service) beginPendingCache(query string) chan struct{} {
	if s == nil || query == "" {
		return nil
	}
	s.pendingCacheMu.Lock()
	defer s.pendingCacheMu.Unlock()
	if s.pendingCache == nil {
		s.pendingCache = make(map[string]chan struct{})
	}
	done := make(chan struct{})
	s.pendingCache[query] = done
	return done
}

func (s *Service) finishPendingCache(query string, done chan struct{}) {
	if s == nil || query == "" || done == nil {
		return
	}
	s.pendingCacheMu.Lock()
	current, ok := s.pendingCache[query]
	if ok && current == done {
		delete(s.pendingCache, query)
	}
	s.pendingCacheMu.Unlock()
	close(done)
}

func (s *Service) waitForPendingCache(ctx context.Context, query string, onProgress func(ProgressUpdate)) bool {
	if s == nil || query == "" {
		return false
	}
	s.pendingCacheMu.Lock()
	done := s.pendingCache[query]
	s.pendingCacheMu.Unlock()
	if done == nil {
		return false
	}
	notifyProgress(onProgress, ProgressUpdate{
		Key:   cacheLookupKey,
		Kind:  ProgressKindStep,
		Text:  "Esperando persistencia de cache semantica para reutilizar resultados recientes",
		State: ProgressPending,
	})
	waitCtx, cancel := context.WithTimeout(ctx, pendingCacheWaitTimeout)
	defer cancel()
	select {
	case <-waitCtx.Done():
		return false
	case <-done:
		return true
	}
}

func (s *Service) search(ctx context.Context, plan SearchPlan, onActivity func(), onProgress func(ProgressUpdate)) ([]Result, error) {
	queries := plan.Queries()
	if len(queries) == 0 {
		return nil, fmt.Errorf("search query is empty")
	}
	perVariantLimit := searchVariantResultLimit(len(queries))
	candidateLimit := searchCandidateLimit(len(queries))

	type searchBatch struct {
		index   int
		query   string
		results []Result
		err     error
	}

	batches := make(chan searchBatch, len(queries))
	var waitGroup sync.WaitGroup
	for index, currentQuery := range queries {
		waitGroup.Add(1)
		go func(index int, currentQuery string) {
			defer waitGroup.Done()
			results, err := s.searchVariant(ctx, currentQuery, index, len(queries), perVariantLimit, onActivity, onProgress)
			select {
			case <-ctx.Done():
			case batches <- searchBatch{index: index, query: currentQuery, results: results, err: err}:
			}
		}(index, currentQuery)
	}

	go func() {
		waitGroup.Wait()
		close(batches)
	}()

	merged := make([]rankedSearchResult, 0, len(queries)*perVariantLimit)
	var firstErr error
	for batch := range batches {
		if batch.err != nil {
			if firstErr == nil {
				firstErr = batch.err
			}
			continue
		}
		for resultIndex, result := range batch.results {
			merged = append(merged, rankedSearchResult{
				Result:      result,
				query:       batch.query,
				queryIndex:  batch.index,
				resultIndex: resultIndex,
				rankScore:   mergedSearchResultScore(result),
			})
		}
	}

	results := selectTopMergedResults(merged, candidateLimit)
	if len(results) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

type rankedSearchResult struct {
	Result
	query       string
	queryIndex  int
	resultIndex int
	rankScore   float64
}

func (s *Service) searchVariant(ctx context.Context, rewrittenQuery string, queryIndex int, totalQueries int, limit int, onActivity func(), onProgress func(ProgressUpdate)) ([]Result, error) {
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
	progressKey := "search-request"
	progressText := endpoint.String()
	if totalQueries > 1 {
		if queryIndex > 0 {
			progressKey = fmt.Sprintf("search-request:%d", queryIndex+1)
		}
		progressText = fmt.Sprintf("Variante %d/%d: %s", queryIndex+1, totalQueries, endpoint.String())
	}
	notifyProgress(onProgress, ProgressUpdate{
		Key:   progressKey,
		Kind:  ProgressKindSearch,
		Text:  progressText,
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
		Key:   progressKey,
		Kind:  ProgressKindSearch,
		Text:  progressText,
		State: ProgressDone,
	})

	results := selectTopResults(decoded.Results, limit)
	return results, nil
}

func desiredSourceCountForPlan(plan SearchPlan) int {
	queryCount := len(plan.Queries())
	if queryCount > 1 {
		return min(queryCount*maxSearchResultsPerVariant, maxSearchVariants*maxSearchResultsPerVariant)
	}
	return maxPrimarySearchResults
}

func searchVariantResultLimit(queryCount int) int {
	if queryCount > 1 {
		return maxSearchResultsPerVariant
	}
	return maxSingleQueryResults
}

func searchCandidateLimit(queryCount int) int {
	if queryCount > 1 {
		return min(queryCount*maxSearchResultsPerVariant, maxSearchVariants*maxSearchResultsPerVariant)
	}
	return maxSearchCandidateResults
}

func selectTopResults(results []Result, limit int) []Result {
	filtered := make([]Result, 0, len(results))
	for _, result := range results {
		trimmedURL := strings.TrimSpace(result.URL)
		if trimmedURL == "" || shouldSkipSearchURL(trimmedURL) {
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

func selectTopMergedResults(results []rankedSearchResult, limit int) []Result {
	if len(results) == 0 {
		return nil
	}
	grouped := make(map[string]rankedSearchResult, len(results))
	for _, result := range results {
		key := strings.ToLower(strings.TrimSpace(result.URL))
		existing, ok := grouped[key]
		if !ok || result.rankScore > existing.rankScore {
			grouped[key] = result
		}
	}
	merged := make([]rankedSearchResult, 0, len(grouped))
	for _, result := range grouped {
		merged = append(merged, result)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].rankScore == merged[j].rankScore {
			if merged[i].queryIndex == merged[j].queryIndex {
				return merged[i].resultIndex < merged[j].resultIndex
			}
			return merged[i].queryIndex < merged[j].queryIndex
		}
		return merged[i].rankScore > merged[j].rankScore
	})
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	selected := make([]Result, 0, len(merged))
	for _, result := range merged {
		selected = append(selected, result.Result)
	}
	return selected
}

func mergedSearchResultScore(result Result) float64 {
	return result.Score
}

func shouldSkipSearchURL(rawURL string) bool {
	if isVideoResult(rawURL) {
		return true
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	ext := strings.ToLower(path.Ext(parsed.Path))
	switch ext {
	case ".7z", ".apk", ".avi", ".csv", ".doc", ".docx", ".dmg", ".exe", ".gif", ".gz", ".iso", ".jpeg", ".jpg", ".mov", ".mp3", ".mp4", ".pdf", ".png", ".ppt", ".pptx", ".rar", ".svg", ".tar", ".tgz", ".webp", ".xls", ".xlsx", ".zip":
		return true
	default:
		return false
	}
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
	if len(results) > maxSearchCandidateResults {
		results = results[:maxSearchCandidateResults]
	}
	fetched, err := s.fetchBatch(ctx, results, onActivity, onProgress)
	if err != nil {
		return nil, 0, err
	}
	documents, processedCount := collectFetchedDocuments(fetched, desiredCount)
	return documents, processedCount, nil
}

func (s *Service) fetchBatch(ctx context.Context, results []Result, onActivity func(), onProgress func(ProgressUpdate)) ([]fetchedDocument, error) {
	fetched := make([]fetchedDocument, len(results))
	if len(results) == 0 {
		return fetched, nil
	}
	group, groupCtx := errgroup.WithContext(ctx)
	jobs := make(chan int)
	workerCount := downloadWorkerCount(len(results))

	group.Go(func() error {
		return dispatchFetchJobs(groupCtx, jobs, len(results))
	})

	for worker := 0; worker < workerCount; worker++ {
		group.Go(func() error {
			return s.fetchBatchWorker(groupCtx, jobs, results, fetched, onActivity, onProgress)
		})
	}

	if err := group.Wait(); err != nil {
		return nil, err
	}

	return fetched, nil
}

func downloadWorkerCount(resultCount int) int {
	workerCount := min(resultCount, maxDownloadWorkers)
	workerCount = min(workerCount, max(1, runtime.GOMAXPROCS(0)))
	if workerCount <= 0 {
		return 1
	}
	return workerCount
}

func dispatchFetchJobs(ctx context.Context, jobs chan<- int, total int) error {
	defer close(jobs)
	for index := 0; index < total; index++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case jobs <- index:
		}
	}
	return nil
}

func (s *Service) fetchBatchWorker(ctx context.Context, jobs <-chan int, results []Result, fetched []fetchedDocument, onActivity func(), onProgress func(ProgressUpdate)) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case index, ok := <-jobs:
			if !ok {
				return nil
			}
			current, err := s.fetchDocument(ctx, results[index], onActivity, onProgress)
			if err != nil {
				return err
			}
			fetched[index] = current
		}
	}
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
			Score:   result.Score,
		},
		sourceMD:  markdownizeSourceContent(article.Content, content),
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

func notifySearchCandidates(onProgress func(ProgressUpdate), results []Result) {
	urls := make([]string, 0, len(results))
	for _, r := range results {
		if u := strings.TrimSpace(r.URL); u != "" {
			urls = append(urls, u)
		}
	}
	notifyProgress(onProgress, ProgressUpdate{
		Key:   "search-candidates",
		Kind:  ProgressKindSearch,
		Text:  strings.Join(urls, "\n"),
		State: ProgressInfo,
	})
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

func classifySearchIntent(query string) SearchIntent {
	normalized := strings.ToLower(strings.TrimSpace(query))
	if normalized == "" {
		return SearchIntentUnknown
	}
	temporalHints := []string{
		"today", "latest", "currently", "current", "news", "recent", "recently", "this week", "this month", "this year",
		"hoy", "actual", "actuales", "actualidad", "ahora", "ultimo", "ultimos", "ultima", "ultimas", "reciente", "recientes", "noticias", "esta semana", "este mes", "este ano", "este año",
	}
	for _, hint := range temporalHints {
		if strings.Contains(normalized, hint) {
			return SearchIntentTemporal
		}
	}
	for year := 2020; year <= 2030; year++ {
		if strings.Contains(normalized, fmt.Sprintf("%d", year)) {
			return SearchIntentTemporal
		}
	}
	return SearchIntentStatic
}

func ExtractSearchQueries(response string) []string {
	ordered := []labeledSearchQuery{{label: "query primaria"}, {label: "query de larga cola"}, {label: "busqueda tecnica"}}
	fallback := parseSearchQueryLines(response, ordered)
	if ordered[0].value == "" && len(fallback) > 0 {
		ordered[0].value = fallback[0]
		fallback = fallback[1:]
	}

	queries := make([]string, 0, maxSearchVariants)
	for _, current := range ordered {
		appendUniqueSearchQuery(&queries, current.value)
	}
	for _, current := range fallback {
		appendUniqueSearchQuery(&queries, current)
		if len(queries) >= maxSearchVariants {
			break
		}
	}
	return queries
}

func parseSearchQueryLines(response string, ordered []labeledSearchQuery) []string {
	lines := strings.Split(strings.TrimSpace(response), "\n")
	fallback := make([]string, 0, len(lines))
	for _, line := range lines {
		candidate := normalizeSearchQueryLine(line)
		if candidate == "" {
			continue
		}
		if !assignLabeledSearchQuery(candidate, ordered) {
			fallback = append(fallback, cleanSearchQueryCandidate(candidate))
		}
	}
	return fallback
}

func assignLabeledSearchQuery(candidate string, ordered []labeledSearchQuery) bool {
	lower := strings.ToLower(candidate)
	for index := range ordered {
		if !strings.HasPrefix(lower, ordered[index].label) {
			continue
		}
		ordered[index].value = extractSearchQueryValue(candidate)
		return true
	}
	return false
}

func extractSearchQueryValue(candidate string) string {
	for _, separator := range []string{":", "-", "="} {
		if position := strings.Index(candidate, separator); position >= 0 {
			return cleanSearchQueryCandidate(candidate[position+1:])
		}
	}
	return ""
}

func appendUniqueSearchQuery(queries *[]string, value string) {
	trimmed := cleanSearchQueryCandidate(value)
	if trimmed == "" {
		return
	}
	for _, existing := range *queries {
		if strings.EqualFold(existing, trimmed) {
			return
		}
	}
	*queries = append(*queries, trimmed)
}

func ExtractPrimarySearchQuery(response string) string {
	queries := ExtractSearchQueries(response)
	if len(queries) == 0 {
		return ""
	}
	return queries[0]
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
		_, _ = fmt.Fprintf(&builder, "\n[%d] %s\n", i+1, safeTitle(document.Title))
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

	builder.WriteString("\nFuentes:\n")
	for index, document := range documents {
		_, _ = fmt.Fprintf(&builder, "- [%d] ", index+1)
		builder.WriteString(document.URL)
		builder.WriteString("\n")
	}
	builder.WriteString("\nInstruccion obligatoria final:\n")
	builder.WriteString("- Usa citas [n] en cada afirmacion factual, incluyendo la oracion inicial.\n")
	builder.WriteString("- La seccion final \"Fuentes:\" debe incluir solo las fuentes realmente citadas en la respuesta, con la misma numeracion.\n")
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

func chunkSource(src Document, idx int) []Chunk {
	text := strings.TrimSpace(src.Content)
	if text == "" {
		return nil
	}

	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}

	step := ChunkSize - ChunkOverlap
	if step <= 0 {
		step = ChunkSize
	}

	chunks := make([]Chunk, 0, max(1, len(runes)/max(1, step)+1))
	for start := 0; start < len(runes); start += step {
		end := min(start+ChunkSize, len(runes))
		chunkText := strings.TrimSpace(string(runes[start:end]))
		if chunkText != "" {
			chunks = append(chunks, Chunk{
				SourceURL: src.URL,
				SourceIdx: idx,
				Text:      chunkText,
				start:     start,
				end:       end,
			})
		}
		if end == len(runes) {
			break
		}
	}

	return chunks
}

func selectRelevantChunks(query string, sources []Document) []Chunk {
	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" || len(sources) == 0 {
		return nil
	}

	queryTerms := rankingTerms(trimmedQuery)
	if len(queryTerms) == 0 {
		return nil
	}

	normalizedQuery := normalizeRankingText(trimmedQuery)
	allChunks := make([]Chunk, 0, len(sources)*2)
	for index, source := range sources {
		for _, chunk := range chunkSource(source, index+1) {
			chunk.Score = scoreChunk(normalizedQuery, queryTerms, source, chunk)
			if chunk.Score <= 0 {
				continue
			}
			allChunks = append(allChunks, chunk)
		}
	}

	sort.SliceStable(allChunks, func(i, j int) bool {
		if allChunks[i].Score == allChunks[j].Score {
			if allChunks[i].SourceIdx == allChunks[j].SourceIdx {
				return allChunks[i].start < allChunks[j].start
			}
			return allChunks[i].SourceIdx < allChunks[j].SourceIdx
		}
		return allChunks[i].Score > allChunks[j].Score
	})

	if len(allChunks) > MaxChunks {
		allChunks = allChunks[:MaxChunks]
	}

	return allChunks
}

func selectRelevantDocumentChunks(query string, documents []Document) ([]Document, int) {
	if len(documents) == 0 {
		return nil, 0
	}

	normalizedDocuments := make([]Document, len(documents))
	for index, document := range documents {
		normalizedDocuments[index] = document
		if normalizedDocuments[index].Index <= 0 {
			normalizedDocuments[index].Index = index + 1
		}
	}

	selectedChunks := selectRelevantChunks(query, normalizedDocuments)
	if len(selectedChunks) == 0 {
		return normalizedDocuments, 0
	}

	chunksBySource := make(map[int][]Chunk, len(normalizedDocuments))
	for _, chunk := range selectedChunks {
		chunksBySource[chunk.SourceIdx] = append(chunksBySource[chunk.SourceIdx], chunk)
	}

	selectedDocuments := make([]Document, 0, len(chunksBySource))
	for index, document := range normalizedDocuments {
		chunks := chunksBySource[index+1]
		if len(chunks) == 0 {
			continue
		}
		document.Content = stitchSelectedChunks(document.Content, chunks)
		selectedDocuments = append(selectedDocuments, document)
	}

	if len(selectedDocuments) == 0 {
		return normalizedDocuments, 0
	}

	return selectedDocuments, len(selectedChunks)
}

func stitchSelectedChunks(content string, chunks []Chunk) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || len(chunks) == 0 {
		return trimmed
	}

	runes := []rune(trimmed)
	merged := mergeChunks(chunks, runes)
	return renderMergedChunks(merged, runes, trimmed)
}

func mergeChunks(chunks []Chunk, runes []rune) []Chunk {
	sort.SliceStable(chunks, func(i, j int) bool {
		if chunks[i].start == chunks[j].start {
			return chunks[i].end < chunks[j].end
		}
		return chunks[i].start < chunks[j].start
	})

	merged := make([]Chunk, 0, len(chunks))
	for _, chunk := range chunks {
		if len(merged) == 0 {
			merged = append(merged, chunk)
			continue
		}
		last := &merged[len(merged)-1]
		if chunk.start <= last.end {
			if chunk.end > last.end {
				last.end = chunk.end
				last.Text = strings.TrimSpace(string(runes[last.start:last.end]))
			}
			continue
		}
		merged = append(merged, chunk)
	}

	return merged
}

func renderMergedChunks(chunks []Chunk, runes []rune, fallback string) string {
	parts := make([]string, 0, len(chunks))
	for index, chunk := range chunks {
		fragment := strings.TrimSpace(string(runes[chunk.start:chunk.end]))
		if fragment == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("[Fragmento %d]\n%s", index+1, fragment))
	}

	if len(parts) == 0 {
		return fallback
	}

	return strings.Join(parts, "\n\n...\n\n")
}

func scoreChunk(normalizedQuery string, queryTerms []string, source Document, chunk Chunk) float64 {
	text := strings.TrimSpace(chunk.Text)
	if text == "" {
		return 0
	}

	chunkTerms := rankingTerms(text)
	if len(chunkTerms) == 0 {
		return 0
	}

	chunkCounts := make(map[string]int, len(chunkTerms))
	for _, term := range chunkTerms {
		chunkCounts[term]++
	}

	uniqueMatches := 0
	totalMatches := 0
	for _, term := range queryTerms {
		if count := chunkCounts[term]; count > 0 {
			uniqueMatches++
			totalMatches += min(count, 3)
		}
	}
	if uniqueMatches == 0 {
		return 0
	}

	coverage := float64(uniqueMatches) / float64(len(queryTerms))
	density := float64(totalMatches) / math.Sqrt(float64(len(chunkTerms)))
	phraseBoost := 0.0
	if normalizedQuery != "" && strings.Contains(normalizeRankingText(text), normalizedQuery) {
		phraseBoost = 1.5
	}
	contextBoost := lexicalCoverage(queryTerms, source.Title+" "+source.Excerpt)
	sourceBoost := source.Score * 0.1
	indexBoost := 1 / float64(max(1, source.Index))

	return coverage*5 + density*2 + contextBoost*1.5 + phraseBoost + sourceBoost + indexBoost*0.25
}

func lexicalCoverage(queryTerms []string, value string) float64 {
	terms := rankingTerms(value)
	if len(queryTerms) == 0 || len(terms) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		seen[term] = struct{}{}
	}
	matches := 0
	for _, term := range queryTerms {
		if _, ok := seen[term]; ok {
			matches++
		}
	}
	return float64(matches) / float64(len(queryTerms))
}

func rankingTerms(value string) []string {
	normalized := normalizeRankingText(value)
	if normalized == "" {
		return nil
	}
	fields := strings.Fields(normalized)
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		if len(field) <= 1 {
			continue
		}
		terms = append(terms, field)
	}
	if len(terms) == 0 {
		return fields
	}
	return terms
}

func normalizeRankingText(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(trimmed))
	lastSpace := false
	for _, current := range trimmed {
		if unicode.IsLetter(current) || unicode.IsDigit(current) {
			builder.WriteRune(current)
			lastSpace = false
			continue
		}
		if lastSpace {
			continue
		}
		builder.WriteByte(' ')
		lastSpace = true
	}

	return strings.Join(strings.Fields(builder.String()), " ")
}

func buildDocumentSummaryPrompt(query string, document Document) string {
	var builder strings.Builder
	builder.WriteString(systemRoleLabel)
	builder.WriteString("Actua como un motor de extraccion basado estrictamente en evidencia. Tu unico proposito es extraer de esta fuente solo lo necesario para responder la consulta original, sin agregar conocimientos previos ni inferencias externas.\n\n")
	builder.WriteString("Contexto y Reglas:\n")
	builder.WriteString("- Fidelidad Absoluta: Usa solo la informacion de esta fuente. Si no alcanza para responder, escribe exactamente: \"")
	builder.WriteString(insufficientInfoMsg)
	builder.WriteString("\".\n")
	builder.WriteString("- Los bloques marcados como [Fragmento n] representan pasajes seleccionados por relevancia dentro de la misma fuente. Trata cada fragmento como evidencia primaria de este documento.\n")
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
	builder.WriteString("Fragmentos relevantes del contenido:\n")
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
		_, _ = fmt.Fprintf(&builder, "\n[%d] %s\n", index+1, safeTitle(summary.Title))
		builder.WriteString("URL: ")
		builder.WriteString(summary.URL)
		builder.WriteString("\n")
		builder.WriteString("Resumen:\n")
		builder.WriteString(strings.TrimSpace(summary.Summary))
		builder.WriteString("\n")
	}

	builder.WriteString("\nFuentes:\n")
	for index, summary := range summaries {
		_, _ = fmt.Fprintf(&builder, "- [%d] ", index+1)
		builder.WriteString(summary.URL)
		builder.WriteString("\n")
	}
	builder.WriteString("\nInstruccion obligatoria final:\n")
	builder.WriteString("- Usa citas [n] en cada afirmacion factual, incluyendo la oracion inicial.\n")
	builder.WriteString("- La seccion final \"Fuentes:\" debe incluir solo las fuentes realmente citadas en la respuesta, con la misma numeracion.\n")
	builder.WriteString("- Antes de terminar, verifica que no exista ninguna afirmacion sin cita y que toda cita usada aparezca en la lista final.\n")

	return builder.String()
}

func appendEvidenceAnswerInstructions(builder *strings.Builder, sourceLabel string) {
	// Usamos un raw string para legibilidad.
	// Menos llamadas a WriteString = menos sufrimiento para el recolector de basura.
	const promptTemplate = `### ROLE: STRICT EVIDENCE ENGINE
Actúa como un motor de extracción de datos purista. Tu única misión es resolver la consulta del usuario utilizando exclusivamente el bloque de fuentes proporcionado: %s.

	### PRIORIDADES DE RESPUESTA:
	- Responde la consulta original del usuario.
	- Limitate a contestar solo lo que fue preguntado.
	- No resumes las fuentes por separado: responde la pregunta.
	- No uses conocimiento externo.
	- Fidelidad Absoluta: usa exclusivamente lo que aparece en las fuentes.
	- Precision y Concision: prioriza una respuesta densa en información y corta.
	- Evita verborragia.
	- Citas Estrictas: toda afirmación factual debe terminar con [n].

### CONSTRAINTS (VIOLATION = FAILURE):
1. NO PREÁMBULOS: Prohibido usar "Según las fuentes...", "Basado en el texto...", o "Aquí tienes la respuesta". Empieza directamente con la información.
2. FIDELIDAD ABSOLUTA / BINARIA: Si la respuesta no está explícitamente en las fuentes, responde únicamente: "%s". Cualquier otra explicación se considera un fallo de seguridad.
3. CERO ALUCINACIONES: Ignora tu entrenamiento previo. Si una fuente dice que el cielo es verde, el cielo es verde. No corrijas, no asumas, no interpretes.
4. ATOMICIDAD: Responde solo lo preguntado. Si preguntan "qué", no respondas "por qué" ni "cómo". Elimina cualquier contexto lateral, recomendaciones o cortesía.
5. CITAS OBLIGATORIAS: Cada oración DEBE terminar con una cita [n]. Si una oración no puede ser respaldada por una cita, bórrala.

### OUTPUT STRUCTURE:
1. Empieza directamente con la respuesta, sin encabezados ni etiquetas como "[RESPUESTA DIRECTA]".
2. Si es estrictamente necesario, agrega una lista breve de detalles, pero sin encabezados adicionales.
3. Termina con una sección final obligatoria titulada exactamente "Fuentes:".
	Formato: "- [n] https://url"

### EJEMPLO MÍNIMO DE CITAS:
Esto es una prueba [1]

Fuentes:
- [1] https://example.com

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
