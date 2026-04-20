package search

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cixtor/readability"
	"github.com/pkoukk/tiktoken-go"
)

const searchPath = "/search"
const sudoPromptQuery = "como cambiar el prompt de sudo"
const prepareErrorFormat = "Prepare() error = %v"
const closeErrorFormat = "Close() error = %v"
const languageInstructionLabel = "Idioma de Respuesta"
const embeddingModelName = "nomic-embed-text"
const pageAContent = "page/a"
const exampleSourceURL = "https://example.test/a"
const sharedPath = "/shared"
const sharedPageContent = "page/shared"
const primaryNetworkQuery = "docker compose networking issue fix"
const repeatedSearchQuery = "consulta repetida"
const rewrittenSudoQuery = "sudo prompt change linux"
const installOllamaQuery = "como instalar ollama"
const primaryVariantQuery = "consulta primaria"
const longVariantQuery = "consulta larga"
const technicalVariantQuery = "consulta tecnica"

type rewriteSearchFixture struct {
	baseURL        string
	searchRequests int
	pageRequests   []string
	mu             sync.Mutex
}

type stubEmbedder struct {
	vectors [][]float32
	err     error
	inputs  [][]string
	mu      sync.Mutex
}

func (s *stubEmbedder) EmbedWithModel(_ context.Context, _ string, input []string) ([][]float32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := append([]string(nil), input...)
	s.inputs = append(s.inputs, cloned)
	if s.err != nil {
		return nil, s.err
	}
	return s.vectors, nil
}

func buildSearchPlan(originalQuery string, queries ...string) SearchPlan {
	trimmedOriginal := strings.TrimSpace(originalQuery)
	trimmedQueries := make([]string, 0, len(queries))
	for _, query := range queries {
		if current := strings.TrimSpace(query); current != "" {
			trimmedQueries = append(trimmedQueries, current)
		}
	}
	if len(trimmedQueries) == 0 {
		trimmedQueries = append(trimmedQueries, trimmedOriginal)
	}
	return SearchPlan{
		OriginalQuery: trimmedOriginal,
		PrimaryQuery:  trimmedQueries[0],
		Variants:      trimmedQueries,
		Intent:        classifySearchIntent(trimmedOriginal),
	}
}

func (s *stubEmbedder) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.inputs)
}

type stubSemanticCache struct {
	lookupFn     func(context.Context, []float32, time.Time) ([]cachedChunk, error)
	ingestFn     func(context.Context, []cachePoint) error
	ingestPoints []cachePoint
	lookupCalls  int
	closed       bool
	mu           sync.Mutex
}

func (s *stubSemanticCache) Lookup(ctx context.Context, vector []float32, now time.Time) ([]cachedChunk, error) {
	s.mu.Lock()
	s.lookupCalls++
	lookupFn := s.lookupFn
	s.mu.Unlock()
	if lookupFn == nil {
		return nil, nil
	}
	return lookupFn(ctx, vector, now)
}

func (s *stubSemanticCache) Ingest(_ context.Context, points []cachePoint) error {
	s.mu.Lock()
	s.ingestPoints = append(s.ingestPoints, points...)
	ingestFn := s.ingestFn
	s.mu.Unlock()
	if ingestFn != nil {
		return ingestFn(context.Background(), points)
	}
	return nil
}

func (s *stubSemanticCache) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *stubSemanticCache) ingestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ingestPoints)
}

func (s *stubSemanticCache) lookupCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookupCalls
}

func (s *stubSemanticCache) latestChunk() (cachedChunk, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.ingestPoints) == 0 {
		return cachedChunk{}, false
	}
	last := s.ingestPoints[len(s.ingestPoints)-1]
	return cachedChunk{
		Title:         last.Title,
		URL:           last.URL,
		Content:       last.Content,
		OriginalQuery: last.OriginalQuery,
		Hash:          last.Hash,
		Timestamp:     time.Unix(last.TimestampUnix, 0).UTC(),
		Score:         0.99,
	}, true
}

func TestSelectTopResultsOrdersByScoreAndLimits(t *testing.T) {
	results := []Result{
		{URL: "https://example.test/2", Score: 0.2},
		{URL: "https://example.test/5", Score: 0.5},
		{URL: "https://example.test/1", Score: 0.1},
		{URL: "https://example.test/4", Score: 0.4},
		{URL: "https://example.test/3", Score: 0.3},
		{URL: "https://example.test/6", Score: 0.6},
	}

	got := selectTopResults(results, 5)
	if len(got) != 5 {
		t.Fatalf("selectTopResults() len = %d, want 5", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Score < got[i].Score {
			t.Fatalf("results are not sorted descending: %+v", got)
		}
	}
	if got[0].URL != "https://example.test/6" {
		t.Fatalf("top result url = %q, want highest score URL", got[0].URL)
	}
}

func TestSelectTopResultsExcludesVideoHostsBeforeLimiting(t *testing.T) {
	results := []Result{
		{URL: "https://www.youtube.com/watch?v=abc", Score: 99},
		{URL: "https://vimeo.com/123", Score: 98},
		{URL: "https://docs.example.test/a", Score: 3},
		{URL: "https://blog.example.test/b", Score: 2},
		{URL: "https://news.example.test/c", Score: 1},
	}

	got := selectTopResults(results, 3)
	if len(got) != 3 {
		t.Fatalf("selectTopResults() len = %d, want 3", len(got))
	}
	for _, result := range got {
		if isVideoResult(result.URL) {
			t.Fatalf("selectTopResults() included video url %q", result.URL)
		}
	}
	if got[0].URL != "https://docs.example.test/a" {
		t.Fatalf("first non-video result = %q, want docs source", got[0].URL)
	}
}

func TestSelectTopMergedResultsOrdersByScoreAndLimits(t *testing.T) {
	results := []rankedSearchResult{
		{Result: Result{URL: "https://example.test/a", Score: 0.65}, queryIndex: 0, resultIndex: 0, rankScore: mergedSearchResultScore(Result{Score: 0.65})},
		{Result: Result{URL: "https://example.test/b", Score: 0.95}, queryIndex: 2, resultIndex: 0, rankScore: mergedSearchResultScore(Result{Score: 0.95})},
		{Result: Result{URL: "https://example.test/c", Score: 0.85}, queryIndex: 1, resultIndex: 0, rankScore: mergedSearchResultScore(Result{Score: 0.85})},
	}

	got := selectTopMergedResults(results, 2)
	if len(got) != 2 {
		t.Fatalf("selectTopMergedResults() len = %d, want 2", len(got))
	}
	if got[0].URL != "https://example.test/b" {
		t.Fatalf("top merged result url = %q, want highest score URL", got[0].URL)
	}
	if got[1].URL != "https://example.test/c" {
		t.Fatalf("second merged result url = %q, want second highest score URL", got[1].URL)
	}
}

func TestSelectTopMergedResultsDeduplicatesUsingHighestScore(t *testing.T) {
	results := []rankedSearchResult{
		{Result: Result{URL: "https://example.test/shared", Score: 0.55, Title: "Lower"}, queryIndex: 0, resultIndex: 0, rankScore: mergedSearchResultScore(Result{Score: 0.55})},
		{Result: Result{URL: "https://example.test/shared", Score: 0.91, Title: "Higher"}, queryIndex: 2, resultIndex: 1, rankScore: mergedSearchResultScore(Result{Score: 0.91})},
		{Result: Result{URL: "https://example.test/other", Score: 0.60, Title: "Other"}, queryIndex: 1, resultIndex: 0, rankScore: mergedSearchResultScore(Result{Score: 0.60})},
	}

	got := selectTopMergedResults(results, 3)
	if len(got) != 2 {
		t.Fatalf("selectTopMergedResults() len = %d, want 2 after dedup", len(got))
	}
	if got[0].Title != "Higher" {
		t.Fatalf("deduplicated result title = %q, want highest score duplicate", got[0].Title)
	}
}

func TestPrepareBuildsSummaryPrompt(t *testing.T) {
	fixture := &rewriteSearchFixture{pageRequests: make([]string, 0, 2)}
	server := httptest.NewServer(http.HandlerFunc(fixture.handleRewriteSearch(t)))
	defer server.Close()
	fixture.baseURL = server.URL

	service := NewService(server.URL + searchPath)
	service.parse = parsePageEcho

	activityCount := 0
	progress := make([]ProgressUpdate, 0, 6)
	prepared, err := service.Prepare(context.Background(), sudoPromptQuery, rewrittenSudoQuery, nil, func() {
		activityCount++
	}, func(update ProgressUpdate) {
		progress = append(progress, update)
	})
	if err != nil {
		t.Fatalf(prepareErrorFormat, err)
	}
	assertBuildPromptResult(t, prepared, server.URL, activityCount, fixture, progress)
}

func TestPrepareUsesSemanticCacheHitBeforeWebSearch(t *testing.T) {
	embedder := &stubEmbedder{vectors: [][]float32{{0.1, 0.2, 0.3}}}
	cache := &stubSemanticCache{lookupFn: func(_ context.Context, vector []float32, _ time.Time) ([]cachedChunk, error) {
		if len(vector) != 3 {
			t.Fatalf("lookup vector len = %d, want 3", len(vector))
		}
		return []cachedChunk{{
			Title:         "Cached sudo docs",
			URL:           "https://cache.example.test/sudo",
			Content:       "sudo prompt can be changed editing the prompt string in sudoers.",
			OriginalQuery: sudoPromptQuery,
			Timestamp:     time.Now().UTC(),
			Score:         0.98,
		}}, nil
	}}
	service := NewService(
		"http://invalid.local/search",
		WithEmbedder(embedder, embeddingModelName),
		withSemanticCacheStore(cache),
	)
	rewriteCalls := 0

	progress := make([]ProgressUpdate, 0, 3)
	prepared, err := service.Prepare(context.Background(), sudoPromptQuery, "", func(_ context.Context, query string) (SearchPlan, error) {
		rewriteCalls++
		return buildSearchPlan(query, query+" rewritten"), nil
	}, nil, func(update ProgressUpdate) {
		progress = append(progress, update)
	})
	if err != nil {
		t.Fatalf(prepareErrorFormat, err)
	}
	if !strings.Contains(prepared.Prompt, "https://cache.example.test/sudo") {
		t.Fatalf("prompt missing cached source url: %q", prepared.Prompt)
	}
	if !strings.Contains(prepared.Prompt, "sudo prompt can be changed") {
		t.Fatalf("prompt missing cached content: %q", prepared.Prompt)
	}
	if embedder.callCount() != 1 {
		t.Fatalf("embed call count = %d, want 1", embedder.callCount())
	}
	if cache.lookupCount() != 1 {
		t.Fatalf("cache lookup count = %d, want 1", cache.lookupCount())
	}
	if rewriteCalls != 0 {
		t.Fatalf("rewrite call count = %d, want 0 on semantic cache hit", rewriteCalls)
	}
	assertProgressContains(t, progress, cacheLookupKey, ProgressDone)
	if err := service.Close(); err != nil {
		t.Fatalf(closeErrorFormat, err)
	}
	if cache.ingestCount() != 0 {
		t.Fatalf("cache ingest count = %d, want 0 for cache hit", cache.ingestCount())
	}
}

func TestMarkdownizePlainTextSeparatesSingleLineParagraphs(t *testing.T) {
	input := "Primer parrafo.\nSegundo parrafo.\nTercer parrafo."

	got := markdownizePlainText(input)
	want := "Primer parrafo.\n\nSegundo parrafo.\n\nTercer parrafo."

	if got != want {
		t.Fatalf("markdownizePlainText() = %q, want %q", got, want)
	}
}

func TestMarkdownizePlainTextJoinsWrappedParagraphLines(t *testing.T) {
	input := "Esta es una linea muy larga que continua\nporque el extractor corto el renglon en dos partes sin terminar la idea.\n\nNuevo parrafo independiente."

	got := markdownizePlainText(input)
	want := "Esta es una linea muy larga que continua porque el extractor corto el renglon en dos partes sin terminar la idea.\n\nNuevo parrafo independiente."

	if got != want {
		t.Fatalf("markdownizePlainText() = %q, want %q", got, want)
	}
}

func TestMarkdownizePlainTextPreservesMarkdownBlocks(t *testing.T) {
	input := strings.Join([]string{
		"# Titulo",
		"",
		"Texto con **bold**.",
		"",
		"```bash",
		"npm test",
		"```",
		"",
		"- uno",
		"- dos",
	}, "\n")

	got := markdownizePlainText(input)

	if !strings.Contains(got, "# Titulo") {
		t.Fatalf("markdownizePlainText() = %q, want heading preserved", got)
	}
	if !strings.Contains(got, "Texto con **bold**.") {
		t.Fatalf("markdownizePlainText() = %q, want inline markdown preserved", got)
	}
	if !strings.Contains(got, "```bash\nnpm test\n```") {
		t.Fatalf("markdownizePlainText() = %q, want fenced code block preserved", got)
	}
	if !strings.Contains(got, "- uno\n- dos") {
		t.Fatalf("markdownizePlainText() = %q, want list preserved", got)
	}
}

func TestPrepareFallsBackToWebSearchWhenSemanticCacheMisses(t *testing.T) {
	baseURL := ""
	embedder := &stubEmbedder{vectors: [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}}}
	cache := &stubSemanticCache{}
	rewriteCalls := 0
	progress := make([]ProgressUpdate, 0, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case searchPath:
			if got := r.URL.Query().Get("q"); got != "consulta optimizada" {
				t.Fatalf("search query = %q, want rewritten fallback query", got)
			}
			fmt.Fprintf(w, `{"results":[{"title":"A","url":"%s/a","content":"snippet a","score":0.8}]}`, baseURL)
		case "/a":
			fmt.Fprint(w, pageAContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	baseURL = server.URL
	service := NewService(
		server.URL+searchPath,
		WithEmbedder(embedder, embeddingModelName),
		withSemanticCacheStore(cache),
	)
	service.parse = parsePageEcho

	prepared, err := service.Prepare(context.Background(), "consulta", "", func(_ context.Context, query string) (SearchPlan, error) {
		rewriteCalls++
		if query != "consulta" {
			t.Fatalf("rewrite query = %q, want original query", query)
		}
		return buildSearchPlan(query, "consulta optimizada"), nil
	}, nil, func(update ProgressUpdate) {
		progress = append(progress, update)
	})
	if err != nil {
		t.Fatalf(prepareErrorFormat, err)
	}
	if !strings.Contains(prepared.Prompt, baseURL+"/a") {
		t.Fatalf("prompt missing fallback web source: %q", prepared.Prompt)
	}
	if !strings.Contains(prepared.Prompt, "Query usada para la busqueda: consulta optimizada") {
		t.Fatalf("prompt missing rewritten fallback query: %q", prepared.Prompt)
	}
	if embedder.callCount() == 0 {
		t.Fatal("expected embedder to be used before fallback web search")
	}
	if rewriteCalls != 1 {
		t.Fatalf("rewrite call count = %d, want 1 on semantic cache miss", rewriteCalls)
	}
	assertProgressContainsText(t, progress, "downloads", ProgressPending, "[consulta optimizada]")
	if err := service.Close(); err != nil {
		t.Fatalf(closeErrorFormat, err)
	}
	if cache.lookupCount() != 1 {
		t.Fatalf("cache lookup count = %d, want 1", cache.lookupCount())
	}
	if cache.ingestCount() != 0 {
		t.Fatalf("cache ingest count = %d, want 0 before deferred persistence", cache.ingestCount())
	}
}

func TestPrepareIncludesAllSearchVariantsInDownloadProgress(t *testing.T) {
	baseURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case searchPath:
			query := r.URL.Query().Get("q")
			switch query {
			case "instalar ollama":
				fmt.Fprintf(w, `{"results":[{"title":"A1","url":"%s/a1","content":"snippet a1","score":0.91},{"title":"A2","url":"%s/a2","content":"snippet a2","score":0.81},{"title":"A3","url":"%s/a3","content":"snippet a3","score":0.71}]}`,
					baseURL,
					baseURL,
					baseURL,
				)
			case installOllamaQuery:
				fmt.Fprintf(w, `{"results":[{"title":"B1","url":"%s/b1","content":"snippet b1","score":0.89},{"title":"B2","url":"%s/b2","content":"snippet b2","score":0.79},{"title":"B3","url":"%s/b3","content":"snippet b3","score":0.69}]}`,
					baseURL,
					baseURL,
					baseURL,
				)
			case "ollama":
				fmt.Fprintf(w, `{"results":[{"title":"C1","url":"%s/c1","content":"snippet c1","score":0.87},{"title":"C2","url":"%s/c2","content":"snippet c2","score":0.77},{"title":"C3","url":"%s/c3","content":"snippet c3","score":0.67}]}`,
					baseURL,
					baseURL,
					baseURL,
				)
			default:
				t.Fatalf("unexpected search query: %q", query)
			}
		case "/a1", "/a2", "/a3", "/b1", "/b2", "/b3", "/c1", "/c2", "/c3":
			fmt.Fprint(w, strings.TrimPrefix(r.URL.Path, "/"))
		case "/a":
			fmt.Fprint(w, pageAContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	baseURL = server.URL

	service := NewService(server.URL + searchPath)
	service.parse = parsePageEcho

	progress := make([]ProgressUpdate, 0, 8)
	_, err := service.Prepare(context.Background(), installOllamaQuery, "", func(_ context.Context, query string) (SearchPlan, error) {
		return buildSearchPlan(query, "instalar ollama", installOllamaQuery, "ollama"), nil
	}, nil, func(update ProgressUpdate) {
		progress = append(progress, update)
	})
	if err != nil {
		t.Fatalf(prepareErrorFormat, err)
	}

	assertProgressContainsText(t, progress, "downloads", ProgressPending, "[instalar ollama, como instalar ollama, ollama]")
	assertProgressContainsText(t, progress, "downloads", ProgressPending, "Descargando hasta 9 candidatos para seleccionar 9 fuentes")
}

func TestPrepareUsesTopThreeResultsPerVariantAsSources(t *testing.T) {
	baseURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case searchPath:
			switch r.URL.Query().Get("q") {
			case primaryVariantQuery:
				fmt.Fprintf(w, `{"results":[{"title":"A1","url":"%s/a1","content":"snippet a1","score":0.92},{"title":"A2","url":"%s/a2","content":"snippet a2","score":0.82},{"title":"A3","url":"%s/a3","content":"snippet a3","score":0.72}]}`,
					baseURL,
					baseURL,
					baseURL,
				)
			case longVariantQuery:
				fmt.Fprintf(w, `{"results":[{"title":"B1","url":"%s/b1","content":"snippet b1","score":0.91},{"title":"B2","url":"%s/b2","content":"snippet b2","score":0.81},{"title":"B3","url":"%s/b3","content":"snippet b3","score":0.71}]}`,
					baseURL,
					baseURL,
					baseURL,
				)
			case technicalVariantQuery:
				fmt.Fprintf(w, `{"results":[{"title":"C1","url":"%s/c1","content":"snippet c1","score":0.90},{"title":"C2","url":"%s/c2","content":"snippet c2","score":0.80},{"title":"C3","url":"%s/c3","content":"snippet c3","score":0.70}]}`,
					baseURL,
					baseURL,
					baseURL,
				)
			default:
				t.Fatalf("unexpected search query: %q", r.URL.Query().Get("q"))
			}
		case "/a1", "/a2", "/a3", "/b1", "/b2", "/b3", "/c1", "/c2", "/c3":
			fmt.Fprint(w, strings.TrimPrefix(r.URL.Path, "/"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	baseURL = server.URL

	service := NewService(server.URL + searchPath)
	service.parse = parsePageEcho

	prepared, err := service.Prepare(context.Background(), "consulta", "", func(_ context.Context, query string) (SearchPlan, error) {
		return buildSearchPlan(query, primaryVariantQuery, longVariantQuery, technicalVariantQuery), nil
	}, nil, nil)
	if err != nil {
		t.Fatalf(prepareErrorFormat, err)
	}
	if len(prepared.Documents) != 9 {
		t.Fatalf("prepared documents len = %d, want 9", len(prepared.Documents))
	}
	hasResult := func(suffix string) bool {
		for _, document := range prepared.Documents {
			if strings.HasSuffix(document.URL, suffix) {
				return true
			}
		}
		return false
	}
	for _, suffix := range []string{"/a1", "/a2", "/a3", "/b1", "/b2", "/b3", "/c1", "/c2", "/c3"} {
		if !hasResult(suffix) {
			t.Fatalf("prepared documents should include %s: %+v", suffix, prepared.Documents)
		}
	}
}

func TestPersistSemanticCacheStoresResultsWhenCalled(t *testing.T) {
	embedder := &stubEmbedder{vectors: [][]float32{{0.1, 0.2, 0.3}}}
	cache := &stubSemanticCache{}
	service := NewService(
		"http://example.test/search",
		WithEmbedder(embedder, embeddingModelName),
		withSemanticCacheStore(cache),
	)

	progress := make([]ProgressUpdate, 0, 3)
	done := service.PersistSemanticCache("consulta", []Document{{Title: "A", URL: exampleSourceURL, Content: "snippet a"}}, func(update ProgressUpdate) {
		progress = append(progress, update)
	})
	<-done

	if cache.ingestCount() == 0 {
		t.Fatal("expected explicit semantic-cache persistence to ingest cache points")
	}
	assertProgressContains(t, progress, cachePersistKey, ProgressPending)
	assertProgressContains(t, progress, cachePersistKey, ProgressDone)
	if err := service.Close(); err != nil {
		t.Fatalf(closeErrorFormat, err)
	}
}

func TestHTMLContentToMarkdownPreservesStructure(t *testing.T) {
	input := strings.Join([]string{
		"<h2>Guia</h2>",
		"<p>Primer <strong>bloque</strong> con <a href=\"https://example.test/docs\">link</a>.</p>",
		"<pre><code class=\"language-bash\">npm test\nmake lint</code></pre>",
		"<ul><li>uno</li><li>dos</li></ul>",
	}, "")

	got := htmlContentToMarkdown(input)

	if !strings.Contains(got, "## Guia") {
		t.Fatalf("htmlContentToMarkdown() = %q, want heading", got)
	}
	if !strings.Contains(got, "Primer **bloque** con [link](https://example.test/docs).") {
		t.Fatalf("htmlContentToMarkdown() = %q, want formatted paragraph", got)
	}
	if !strings.Contains(got, "```bash\nnpm test\nmake lint\n```") {
		t.Fatalf("htmlContentToMarkdown() = %q, want fenced code block", got)
	}
	if !strings.Contains(got, "- uno\n- dos") {
		t.Fatalf("htmlContentToMarkdown() = %q, want list", got)
	}
	if !strings.Contains(got, "\n\nPrimer **bloque**") {
		t.Fatalf("htmlContentToMarkdown() = %q, want paragraph separation", got)
	}
}

func TestFetchSourceBuildsReadableMarkdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a":
			fmt.Fprint(w, pageAContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	service := NewService(server.URL + searchPath)
	service.parse = parsePageEcho

	source, err := service.FetchSource(context.Background(), server.URL+"/a", "", nil, nil)
	if err != nil {
		t.Fatalf("FetchSource() error = %v", err)
	}
	if source.Document.URL != server.URL+"/a" {
		t.Fatalf("source.Document.URL = %q, want %q", source.Document.URL, server.URL+"/a")
	}
	if !strings.Contains(source.Markdown, "# Title for "+server.URL+"/a") {
		t.Fatalf("source.Markdown = %q, want heading with parsed title", source.Markdown)
	}
	if !strings.Contains(source.Markdown, "Fuente: "+server.URL+"/a") {
		t.Fatalf("source.Markdown = %q, want source URL", source.Markdown)
	}
	if !strings.Contains(source.Markdown, "> excerpt "+pageAContent) {
		t.Fatalf("source.Markdown = %q, want excerpt blockquote", source.Markdown)
	}
	if !strings.Contains(source.Markdown, "## Seccion") {
		t.Fatalf("source.Markdown = %q, want heading from HTML content", source.Markdown)
	}
	if !strings.Contains(source.Markdown, "Parrafo con **importante** y [referencia](https://example.test/ref).") {
		t.Fatalf("source.Markdown = %q, want formatted paragraph from HTML content", source.Markdown)
	}
	if !strings.Contains(source.Markdown, "```bash\ncontent "+pageAContent+"\n```") {
		t.Fatalf("source.Markdown = %q, want fenced code block from HTML content", source.Markdown)
	}
}

func TestPersistSemanticCacheReportsUnderlyingIngestError(t *testing.T) {
	embedder := &stubEmbedder{vectors: [][]float32{{0.1, 0.2, 0.3}}}
	cache := &stubSemanticCache{ingestFn: func(_ context.Context, _ []cachePoint) error {
		return fmt.Errorf("unauthorized")
	}}
	service := NewService(
		"http://example.test/search",
		WithEmbedder(embedder, embeddingModelName),
		withSemanticCacheStore(cache),
	)

	progress := make([]ProgressUpdate, 0, 3)
	done := service.PersistSemanticCache("consulta", []Document{{Title: "A", URL: exampleSourceURL, Content: "snippet a"}}, func(update ProgressUpdate) {
		progress = append(progress, update)
	})
	<-done

	assertProgressContainsText(t, progress, cachePersistKey, ProgressInfo, "unauthorized")
	if err := service.Close(); err != nil {
		t.Fatalf(closeErrorFormat, err)
	}
}

func TestCachePointUUIDProducesValidDeterministicUUID(t *testing.T) {
	input := hashChunk("contenido de prueba")
	first := cachePointUUID(input)
	second := cachePointUUID(input)
	if first != second {
		t.Fatalf("cachePointUUID() = %q and %q, want deterministic value", first, second)
	}
	parts := strings.Split(first, "-")
	if len(parts) != 5 {
		t.Fatalf("cachePointUUID() = %q, want UUID with 5 parts", first)
	}
	if len(parts[0]) != 8 || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 4 || len(parts[4]) != 12 {
		t.Fatalf("cachePointUUID() = %q, want canonical UUID lengths", first)
	}
	if parts[2][0] != '5' {
		t.Fatalf("cachePointUUID() = %q, want version 5 UUID", first)
	}
	if parts[3][0] != 'a' {
		t.Fatalf("cachePointUUID() = %q, want RFC variant nibble", first)
	}
}

func TestCachePointUUIDHashesNonHexInputs(t *testing.T) {
	value := cachePointUUID("copilot-diagnostic-point")
	parts := strings.Split(value, "-")
	if len(parts) != 5 {
		t.Fatalf("cachePointUUID() = %q, want UUID with 5 parts", value)
	}
}

func TestPrepareWaitsForPendingSemanticCacheIngestBeforeRepeatingWebSearch(t *testing.T) {
	baseURL := ""
	searchRequests := 0
	var searchMu sync.Mutex
	cache := &stubSemanticCache{}
	cache.lookupFn = func(_ context.Context, _ []float32, _ time.Time) ([]cachedChunk, error) {
		if chunk, ok := cache.latestChunk(); ok {
			return []cachedChunk{chunk}, nil
		}
		return nil, nil
	}
	cache.ingestFn = func(_ context.Context, _ []cachePoint) error {
		time.Sleep(150 * time.Millisecond)
		return nil
	}
	embedder := &stubEmbedder{vectors: [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}, {0.1, 0.2, 0.3}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case searchPath:
			searchMu.Lock()
			searchRequests++
			searchMu.Unlock()
			fmt.Fprintf(w, `{"results":[{"title":"A","url":"%s/a","content":"snippet a","score":0.8}]}`, baseURL)
		case "/a":
			fmt.Fprint(w, pageAContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	baseURL = server.URL

	service := NewService(
		server.URL+searchPath,
		WithEmbedder(embedder, embeddingModelName),
		withSemanticCacheStore(cache),
	)
	service.parse = parsePageEcho

	firstPrepared, err := service.Prepare(context.Background(), repeatedSearchQuery, repeatedSearchQuery, nil, nil, nil)
	if err != nil {
		t.Fatalf("first Prepare() error = %v", err)
	}
	if !strings.Contains(firstPrepared.Prompt, baseURL+"/a") {
		t.Fatalf("first prompt missing web source: %q", firstPrepared.Prompt)
	}
	<-service.PersistSemanticCache(firstPrepared.CacheQuery, firstPrepared.CacheDocs, nil)

	secondProgress := make([]ProgressUpdate, 0, 4)
	secondPrepared, err := service.Prepare(context.Background(), repeatedSearchQuery, repeatedSearchQuery, nil, nil, func(update ProgressUpdate) {
		secondProgress = append(secondProgress, update)
	})
	if err != nil {
		t.Fatalf("second Prepare() error = %v", err)
	}
	searchMu.Lock()
	if searchRequests != 1 {
		t.Fatalf("search requests = %d, want 1 when second query reuses pending semantic cache", searchRequests)
	}
	searchMu.Unlock()
	if !strings.Contains(secondPrepared.Prompt, baseURL+"/a") {
		t.Fatalf("second prompt missing cached source: %q", secondPrepared.Prompt)
	}
	assertProgressContains(t, secondProgress, cacheLookupKey, ProgressDone)
	if err := service.Close(); err != nil {
		t.Fatalf(closeErrorFormat, err)
	}
}

func TestPrepareFallsBackToWebSearchWhenSemanticCacheEntryExpired(t *testing.T) {
	baseURL := ""
	embedder := &stubEmbedder{vectors: [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}}}
	expiredAt := time.Now().UTC().Add(-72 * time.Hour)
	cache := &stubSemanticCache{lookupFn: func(_ context.Context, _ []float32, now time.Time) ([]cachedChunk, error) {
		entry := cachedChunk{
			Title:         "Old cached doc",
			URL:           "https://cache.example.test/expired",
			Content:       "stale content",
			OriginalQuery: "consulta",
			Timestamp:     expiredAt,
			Score:         0.99,
		}
		if now.Sub(entry.Timestamp) > 48*time.Hour {
			return nil, nil
		}
		return []cachedChunk{entry}, nil
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case searchPath:
			fmt.Fprintf(w, `{"results":[{"title":"A","url":"%s/a","content":"snippet a","score":0.8}]}`, baseURL)
		case "/a":
			fmt.Fprint(w, pageAContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	baseURL = server.URL
	service := NewService(
		server.URL+searchPath,
		WithEmbedder(embedder, embeddingModelName),
		withSemanticCacheStore(cache),
	)
	service.parse = parsePageEcho

	prepared, err := service.Prepare(context.Background(), "consulta", "consulta", nil, nil, nil)
	if err != nil {
		t.Fatalf(prepareErrorFormat, err)
	}
	if strings.Contains(prepared.Prompt, "cache.example.test/expired") {
		t.Fatalf("prompt should not include expired cache entry: %q", prepared.Prompt)
	}
	if !strings.Contains(prepared.Prompt, baseURL+"/a") {
		t.Fatalf("prompt missing fallback web source after ttl expiry: %q", prepared.Prompt)
	}
	if err := service.Close(); err != nil {
		t.Fatalf(closeErrorFormat, err)
	}
}

func TestPrepareReturnsErrorWhenNoSourcesCanBeProcessed(t *testing.T) {
	baseURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case searchPath:
			fmt.Fprintf(w, `{"results":[{"title":"Fallback","url":"%s/article","content":"search snippet","score":1}]}`, baseURL)
		case "/article":
			fmt.Fprint(w, "broken body")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	baseURL = server.URL

	service := NewService(server.URL + searchPath)
	service.parse = func(io.Reader, string) (readability.Article, error) {
		return readability.Article{}, fmt.Errorf("boom")
	}

	_, err := service.Prepare(context.Background(), "consulta", "consulta", nil, nil, nil)
	if err == nil {
		t.Fatal("Prepare() error = nil, want source processing failure")
	}
	if !strings.Contains(err.Error(), "could not extract readable content") {
		t.Fatalf("Prepare() error = %v, want source processing failure", err)
	}
}

func TestPrepareUsesBackupSourcesWhenPrimaryDownloadsFail(t *testing.T) {
	baseURL := ""
	progress := make([]ProgressUpdate, 0, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case searchPath:
			fmt.Fprintf(w, `{"results":[{"title":"A","url":"%s/a","content":"snippet a","score":8},{"title":"B","url":"%s/b","content":"snippet b","score":7},{"title":"C","url":"%s/c","content":"snippet c","score":6},{"title":"D","url":"%s/d","content":"snippet d","score":5},{"title":"E","url":"%s/e","content":"snippet e","score":4},{"title":"F","url":"%s/f","content":"snippet f","score":3},{"title":"Video","url":"https://www.youtube.com/watch?v=abc","content":"video snippet","score":99}]}`,
				baseURL,
				baseURL,
				baseURL,
				baseURL,
				baseURL,
				baseURL,
			)
		case "/b":
			fmt.Fprint(w, "page/b")
		case "/c":
			fmt.Fprint(w, "page/c")
		case "/d":
			fmt.Fprint(w, "page/d")
		case "/e":
			fmt.Fprint(w, "page/e")
		case "/f":
			fmt.Fprint(w, "page/f")
		case "/a":
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, "upstream error")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	baseURL = server.URL

	service := NewService(server.URL + searchPath)
	service.parse = parsePageEcho

	prepared, err := service.Prepare(context.Background(), "consulta", "consulta", nil, nil, func(update ProgressUpdate) {
		progress = append(progress, update)
	})
	if err != nil {
		t.Fatalf(prepareErrorFormat, err)
	}
	if len(prepared.Documents) != 5 {
		t.Fatalf("prepared documents len = %d, want 5", len(prepared.Documents))
	}
	if strings.Contains(prepared.Prompt, baseURL+"/a") {
		t.Fatalf("prompt should not include failed primary source: %q", prepared.Prompt)
	}
	if !strings.Contains(prepared.Prompt, "content page/f") {
		t.Fatalf("prompt missing promoted backup source: %q", prepared.Prompt)
	}
	if strings.Contains(prepared.Prompt, "youtube.com") {
		t.Fatalf("prompt should not include filtered video source: %q", prepared.Prompt)
	}
	assertProcessedSourcesCount(t, progress, 5)
}

func newMultiplexedSearchHandler(t *testing.T, baseURL *string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case searchPath:
			serveMultiplexedSearchResults(t, w, r, *baseURL)
		case "/a":
			fmt.Fprint(w, pageAContent)
		case sharedPath:
			fmt.Fprint(w, sharedPageContent)
		case "/docs":
			fmt.Fprint(w, "page/docs")
		case "/c":
			fmt.Fprint(w, "page/c")
		default:
			http.NotFound(w, r)
		}
	}
}

func serveMultiplexedSearchResults(t *testing.T, w http.ResponseWriter, r *http.Request, baseURL string) {
	t.Helper()
	switch r.URL.Query().Get("q") {
	case primaryVariantQuery:
		fmt.Fprintf(w, `{"results":[{"title":"A","url":"%s/a","content":"snippet a","score":0.9},{"title":"B","url":"%s/shared","content":"snippet shared","score":0.8}]}`,
			baseURL,
			baseURL,
		)
	case longVariantQuery:
		fmt.Fprintf(w, `{"results":[{"title":"Shared alt","url":"%s/shared","content":"snippet shared alt","score":0.95},{"title":"C","url":"%s/c","content":"snippet c","score":0.7}]}`,
			baseURL,
			baseURL,
		)
	case technicalVariantQuery:
		fmt.Fprintf(w, `{"results":[{"title":"Docs","url":"%s/docs","content":"docs","score":0.6}]}`, baseURL)
	default:
		t.Fatalf("unexpected multiplexed query: %q", r.URL.Query().Get("q"))
	}
}

func TestPrepareMultiplexesSearchQueriesAndDeduplicatesResults(t *testing.T) {
	baseURL := ""
	server := httptest.NewServer(newMultiplexedSearchHandler(t, &baseURL))
	defer server.Close()
	baseURL = server.URL

	service := NewService(server.URL + searchPath)
	service.parse = parsePageEcho

	prepared, err := service.Prepare(context.Background(), "consulta", "", func(_ context.Context, query string) (SearchPlan, error) {
		return buildSearchPlan(query, primaryVariantQuery, longVariantQuery, technicalVariantQuery), nil
	}, nil, nil)
	if err != nil {
		t.Fatalf(prepareErrorFormat, err)
	}
	if !strings.Contains(prepared.Prompt, baseURL+sharedPath) {
		t.Fatalf("prompt missing merged shared source: %q", prepared.Prompt)
	}
	sharedCount := 0
	for _, document := range prepared.Documents {
		if document.URL == baseURL+sharedPath {
			sharedCount++
		}
	}
	if sharedCount != 1 {
		t.Fatalf("prepared documents should deduplicate shared source, got %d entries: %+v", sharedCount, prepared.Documents)
	}
	if !strings.Contains(prepared.Prompt, "Query usada para la busqueda: "+primaryVariantQuery) {
		t.Fatalf("prompt missing primary query label: %q", prepared.Prompt)
	}
	if len(prepared.Documents) == 0 {
		t.Fatal("expected prepared documents from multiplexed search")
	}
	for _, document := range prepared.Documents {
		if strings.HasSuffix(document.URL, ".pdf") {
			t.Fatalf("prepared documents should skip heavy file URLs: %+v", prepared.Documents)
		}
	}
}

func TestPreparedPromptBuildsReducedFinalPrompt(t *testing.T) {
	prepared := PreparedPrompt{Query: "consulta"}
	prompt := prepared.BuildFinalPrompt([]SourceSummary{{Title: "Fuente A", URL: exampleSourceURL, Summary: "dato A"}})

	if !strings.Contains(prompt, "System Role:") {
		t.Fatalf("final prompt missing system role section: %q", prompt)
	}
	if !strings.Contains(prompt, "Resúmenes por fuente") {
		t.Fatalf("final prompt missing source summaries section: %q", prompt)
	}
	if !strings.Contains(prompt, "Citas Estrictas") {
		t.Fatalf("final prompt missing citation rule: %q", prompt)
	}
	if !strings.Contains(prompt, "Responde la consulta original del usuario") {
		t.Fatalf("final prompt missing original question instruction: %q", prompt)
	}
	if !strings.Contains(prompt, "Limitate a contestar solo lo que fue preguntado") {
		t.Fatalf("final prompt missing strict scope rule: %q", prompt)
	}
	if !strings.Contains(prompt, "No resumes las fuentes por separado: responde la pregunta") {
		t.Fatalf("final prompt missing direct answer instruction: %q", prompt)
	}
	if !strings.Contains(prompt, "No uses conocimiento externo") {
		t.Fatalf("final prompt missing no-external-knowledge rule: %q", prompt)
	}
	if !strings.Contains(prompt, languageInstructionLabel) {
		t.Fatalf("final prompt missing language instruction: %q", prompt)
	}
	if !strings.Contains(prompt, "dato A") {
		t.Fatalf("final prompt missing source summary content: %q", prompt)
	}
	if !strings.Contains(prompt, "Fuentes:") {
		t.Fatalf("final prompt missing consulted sources section: %q", prompt)
	}
	if strings.Contains(prompt, "1. [RESPUESTA DIRECTA]:") {
		t.Fatalf("final prompt should not require the old response placeholder structure: %q", prompt)
	}
	if strings.Contains(prompt, "[FUENTES CONSULTADAS]:") {
		t.Fatalf("final prompt should not require the old sources placeholder structure: %q", prompt)
	}
	if !strings.Contains(prompt, "- [1] "+exampleSourceURL) {
		t.Fatalf("final prompt missing numbered source URL: %q", prompt)
	}
	if !strings.Contains(prompt, "Esto es una prueba [1]") {
		t.Fatalf("final prompt missing explicit footer example: %q", prompt)
	}
	if !strings.Contains(prompt, "- [1] https://example.com") {
		t.Fatalf("final prompt missing bullet list example: %q", prompt)
	}
}

func TestPreparedPromptRequiresReductionWhenTokenLimitExceeded(t *testing.T) {
	prepared := PreparedPrompt{ApproxTokens: MaxPromptTokens + 1}

	if !prepared.RequiresReduction(MaxPromptTokens) {
		t.Fatal("RequiresReduction() = false, want true when token limit is exceeded")
	}
}

func TestApproximateTokenCountUsesTiktokenEncoding(t *testing.T) {
	text := "Hello, world!"
	encoder, err := tiktoken.GetEncoding(tiktoken.MODEL_CL100K_BASE)
	if err != nil {
		t.Fatalf("GetEncoding() error = %v", err)
	}

	got := ApproximateTokenCount(text)
	want := len(encoder.Encode(text, nil, nil))
	if got != want {
		t.Fatalf("ApproximateTokenCount() = %d, want %d", got, want)
	}
}

func TestBuildSearchRewritePromptIncludesExpectedInstructions(t *testing.T) {
	prompt := BuildSearchRewritePrompt("como arreglo error de docker compose")

	if !strings.Contains(prompt, "Ingeniero de SEO Senior") {
		t.Fatalf("rewrite prompt missing role: %q", prompt)
	}
	if !strings.Contains(prompt, "Query Primaria") {
		t.Fatalf("rewrite prompt missing primary query format: %q", prompt)
	}
	if !strings.Contains(prompt, "Busqueda Tecnica") {
		t.Fatalf("rewrite prompt missing technical query format: %q", prompt)
	}
	if !strings.Contains(prompt, "como arreglo error de docker compose") {
		t.Fatalf("rewrite prompt missing user query: %q", prompt)
	}
}

func TestPreparedPromptBuildsDocumentPromptWithLanguageInstruction(t *testing.T) {
	prepared := PreparedPrompt{Query: "what's a bot?"}
	prompt := prepared.BuildDocumentPrompt(Document{
		Title:   "Bot definition",
		URL:     "https://example.test/bot",
		Excerpt: "A bot is software.",
		Content: "A bot is a software agent that automates tasks.",
	})

	if !strings.Contains(prompt, languageInstructionLabel) {
		t.Fatalf("document prompt missing language instruction: %q", prompt)
	}
	if !strings.Contains(prompt, "extraer de esta fuente solo lo necesario para responder la consulta original") {
		t.Fatalf("document prompt missing extraction goal: %q", prompt)
	}
	if !strings.Contains(prompt, "No redactes una respuesta final al usuario") {
		t.Fatalf("document prompt missing evidence-only rule: %q", prompt)
	}
	if !strings.Contains(prompt, "what's a bot?") {
		t.Fatalf("document prompt missing original query: %q", prompt)
	}
}

func TestExtractPrimarySearchQueryPrefersPrimaryLabel(t *testing.T) {
	response := "Query Primaria: docker compose networking issue fix\nQuery de Larga Cola: docker compose networking issue fix ubuntu 24.04\nBusqueda Tecnica: \"docker compose\" AND networking AND issue"

	got := ExtractPrimarySearchQuery(response)
	if got != primaryNetworkQuery {
		t.Fatalf("ExtractPrimarySearchQuery() = %q, want primary query", got)
	}
}

func TestExtractPrimarySearchQueryFallsBackToFirstNonEmptyLine(t *testing.T) {
	response := "\n  \"docker compose networking issue fix\"  \n\nQuery de Larga Cola: algo"

	got := ExtractPrimarySearchQuery(response)
	if got != primaryNetworkQuery {
		t.Fatalf("ExtractPrimarySearchQuery() = %q, want first non-empty line", got)
	}
}

func TestExtractSearchQueriesReturnsOrderedVariants(t *testing.T) {
	response := "Query Primaria: docker compose networking issue fix\nQuery de Larga Cola: docker compose networking issue fix ubuntu 24.04\nBusqueda Tecnica: \"docker compose\" AND networking AND issue"

	got := ExtractSearchQueries(response)
	want := []string{
		primaryNetworkQuery,
		"docker compose networking issue fix ubuntu 24.04",
		"docker compose\" AND networking AND issue",
	}
	if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Fatalf("ExtractSearchQueries() = %q, want %q", got, want)
	}
}

func (f *rewriteSearchFixture) handleRewriteSearch(t *testing.T) func(http.ResponseWriter, *http.Request) {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case searchPath:
			f.searchRequests++
			if got := r.URL.Query().Get("q"); got != rewrittenSudoQuery {
				t.Fatalf("search query = %q, want rewritten query", got)
			}
			fmt.Fprintf(w, `{"results":[{"title":"A","url":"%s/a","content":"snippet a","score":0.4},{"title":"B","url":"%s/b","content":"snippet b","score":0.9}]}`,
				f.baseURL,
				f.baseURL,
			)
		case "/a", "/b":
			f.mu.Lock()
			f.pageRequests = append(f.pageRequests, r.URL.Path)
			f.mu.Unlock()
			fmt.Fprintf(w, "page%s", r.URL.Path)
		default:
			http.NotFound(w, r)
		}
	}
}

func parsePageEcho(input io.Reader, pageURL string) (readability.Article, error) {
	payload, err := io.ReadAll(input)
	if err != nil {
		return readability.Article{}, err
	}
	return readability.Article{
		Title:       "Title for " + pageURL,
		Excerpt:     "excerpt " + string(payload),
		Content:     "<h2>Seccion</h2><p>Parrafo con <strong>importante</strong> y <a href=\"https://example.test/ref\">referencia</a>.</p><pre><code class=\"language-bash\">content " + string(payload) + "</code></pre>",
		TextContent: "content " + string(payload),
	}, nil
}

func assertBuildPromptResult(t *testing.T, prepared PreparedPrompt, serverURL string, activityCount int, fixture *rewriteSearchFixture, progress []ProgressUpdate) {
	t.Helper()
	if activityCount == 0 {
		t.Fatal("expected build prompt activity callback to be invoked")
	}
	if fixture.searchRequests != 1 {
		t.Fatalf("search requests = %d, want 1", fixture.searchRequests)
	}
	fixture.mu.Lock()
	pageRequests := append([]string(nil), fixture.pageRequests...)
	fixture.mu.Unlock()
	if len(pageRequests) != 2 {
		t.Fatalf("page requests = %d, want 2", len(pageRequests))
	}
	if prepared.ApproxTokens <= 0 {
		t.Fatalf("token count = %d, want a positive value", prepared.ApproxTokens)
	}
	if len(prepared.Documents) != 2 {
		t.Fatalf("prepared documents len = %d, want 2", len(prepared.Documents))
	}
	assertPromptContains(t, prepared.Prompt, serverURL)
	assertProgressUpdates(t, progress, serverURL)
}

func assertProgressContains(t *testing.T, progress []ProgressUpdate, key string, state ProgressState) {
	t.Helper()
	for _, update := range progress {
		if update.Key == key && update.State == state {
			return
		}
	}
	t.Fatalf("progress missing key=%q state=%q in %+v", key, state, progress)
}

func assertProgressContainsText(t *testing.T, progress []ProgressUpdate, key string, state ProgressState, text string) {
	t.Helper()
	for _, update := range progress {
		if update.Key == key && update.State == state && strings.Contains(update.Text, text) {
			return
		}
	}
	t.Fatalf("progress missing key=%q state=%q text containing %q in %+v", key, state, text, progress)
}

func assertPromptContains(t *testing.T, prompt string, serverURL string) {
	t.Helper()
	requiredSubstrings := []string{
		"Consulta original: como cambiar el prompt de sudo",
		"Query usada para la busqueda: " + rewrittenSudoQuery,
		"System Role:",
		"Fidelidad Absoluta",
		"Responde la consulta original del usuario",
		"Limitate a contestar solo lo que fue preguntado",
		"No resumes las fuentes por separado: responde la pregunta",
		"Precision y Concision",
		"Evita verborragia",
		languageInstructionLabel,
		"Fuentes:",
		"- [1] " + serverURL + "/b",
		"- [2] " + serverURL + "/a",
		"Esto es una prueba [1]",
		"Empieza directamente con la respuesta, sin encabezados ni etiquetas como \"[RESPUESTA DIRECTA]\".",
		"- [1] https://example.com",
		serverURL + "/b",
		"content page/b",
	}
	for _, expected := range requiredSubstrings {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing %q: %q", expected, prompt)
		}
	}
}

func assertProgressUpdates(t *testing.T, progress []ProgressUpdate, serverURL string) {
	t.Helper()
	if len(progress) == 0 {
		t.Fatal("expected progress updates to be emitted")
	}
	if progress[0].Key != "query" {
		t.Fatalf("first progress key = %q, want query", progress[0].Key)
	}
	foundQuery := false
	foundSearchURL := false
	foundDownload := false
	for _, update := range progress {
		if update.Key == "query" && strings.Contains(update.Text, rewrittenSudoQuery) {
			foundQuery = true
		}
		if update.Key == "search-request" && strings.Contains(update.Text, serverURL+searchPath) {
			foundSearchURL = true
		}
		if strings.HasPrefix(update.Key, "download:") && update.State == ProgressDone {
			foundDownload = true
		}
	}
	if !foundQuery {
		t.Fatalf("progress updates missing query step: %+v", progress)
	}
	if !foundSearchURL {
		t.Fatalf("progress updates missing search URL: %+v", progress)
	}
	if !foundDownload {
		t.Fatalf("progress updates missing completed download: %+v", progress)
	}
}

func assertProcessedSourcesCount(t *testing.T, progress []ProgressUpdate, want int) {
	t.Helper()
	wantText := fmt.Sprintf("Fuentes procesadas: %d", want)
	for _, update := range progress {
		if update.Key == "downloads" && update.State == ProgressDone && update.Text == wantText {
			return
		}
	}
	t.Fatalf("progress updates missing processed sources count %q: %+v", wantText, progress)
}
