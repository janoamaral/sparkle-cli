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

func (s *stubEmbedder) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.inputs)
}

type stubSemanticCache struct {
	lookupFn     func(context.Context, []float32, time.Time) ([]cachedChunk, error)
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
	defer s.mu.Unlock()
	s.ingestPoints = append(s.ingestPoints, points...)
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

func TestPrepareBuildsSummaryPrompt(t *testing.T) {
	fixture := &rewriteSearchFixture{pageRequests: make([]string, 0, 2)}
	server := httptest.NewServer(http.HandlerFunc(fixture.handleRewriteSearch(t)))
	defer server.Close()
	fixture.baseURL = server.URL

	service := NewService(server.URL + searchPath)
	service.parse = parsePageEcho

	activityCount := 0
	progress := make([]ProgressUpdate, 0, 6)
	prepared, err := service.Prepare(context.Background(), sudoPromptQuery, "sudo prompt change linux", nil, func() {
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
	prepared, err := service.Prepare(context.Background(), sudoPromptQuery, "", func(_ context.Context, query string) (string, error) {
		rewriteCalls++
		return query + " rewritten", nil
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

func TestPrepareFallsBackToWebSearchWhenSemanticCacheMisses(t *testing.T) {
	baseURL := ""
	embedder := &stubEmbedder{vectors: [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}}}
	cache := &stubSemanticCache{}
	rewriteCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case searchPath:
			if got := r.URL.Query().Get("q"); got != "consulta optimizada" {
				t.Fatalf("search query = %q, want rewritten fallback query", got)
			}
			fmt.Fprintf(w, `{"results":[{"title":"A","url":"%s/a","content":"snippet a","score":0.8}]}`, baseURL)
		case "/a":
			fmt.Fprint(w, "page/a")
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

	prepared, err := service.Prepare(context.Background(), "consulta", "", func(_ context.Context, query string) (string, error) {
		rewriteCalls++
		if query != "consulta" {
			t.Fatalf("rewrite query = %q, want original query", query)
		}
		return "consulta optimizada", nil
	}, nil, nil)
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
	if err := service.Close(); err != nil {
		t.Fatalf(closeErrorFormat, err)
	}
	if cache.lookupCount() != 1 {
		t.Fatalf("cache lookup count = %d, want 1", cache.lookupCount())
	}
	if cache.ingestCount() == 0 {
		t.Fatal("expected fallback web search to schedule background cache ingest")
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
			fmt.Fprint(w, "page/a")
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

func TestPreparedPromptBuildsReducedFinalPrompt(t *testing.T) {
	prepared := PreparedPrompt{Query: "consulta"}
	prompt := prepared.BuildFinalPrompt([]SourceSummary{{Title: "Fuente A", URL: "https://example.test/a", Summary: "dato A"}})

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
	if !strings.Contains(prompt, "Fuentes Consultadas") {
		t.Fatalf("final prompt missing consulted sources section: %q", prompt)
	}
	if !strings.Contains(prompt, "- [1] https://example.test/a") {
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
	if got != "docker compose networking issue fix" {
		t.Fatalf("ExtractPrimarySearchQuery() = %q, want primary query", got)
	}
}

func TestExtractPrimarySearchQueryFallsBackToFirstNonEmptyLine(t *testing.T) {
	response := "\n  \"docker compose networking issue fix\"  \n\nQuery de Larga Cola: algo"

	got := ExtractPrimarySearchQuery(response)
	if got != "docker compose networking issue fix" {
		t.Fatalf("ExtractPrimarySearchQuery() = %q, want first non-empty line", got)
	}
}

func (f *rewriteSearchFixture) handleRewriteSearch(t *testing.T) func(http.ResponseWriter, *http.Request) {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case searchPath:
			f.searchRequests++
			if got := r.URL.Query().Get("q"); got != "sudo prompt change linux" {
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

func assertPromptContains(t *testing.T, prompt string, serverURL string) {
	t.Helper()
	requiredSubstrings := []string{
		"Consulta original: como cambiar el prompt de sudo",
		"Query usada para la busqueda: sudo prompt change linux",
		"System Role:",
		"Fidelidad Absoluta",
		"Responde la consulta original del usuario",
		"Limitate a contestar solo lo que fue preguntado",
		"No resumes las fuentes por separado: responde la pregunta",
		"Precision y Concision",
		"Evita verborragia",
		languageInstructionLabel,
		"Fuentes Consultadas",
		"- [1] " + serverURL + "/b",
		"- [2] " + serverURL + "/a",
		"Esto es una prueba [1]",
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
		if update.Key == "query" && strings.Contains(update.Text, sudoPromptQuery) {
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
