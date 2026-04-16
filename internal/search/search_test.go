package search

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cixtor/readability"
)

const searchPath = "/search"
const sudoPromptQuery = "como cambiar el prompt de sudo"

type rewriteSearchFixture struct {
	baseURL        string
	searchRequests int
	pageRequests   []string
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

func TestPrepareBuildsSummaryPrompt(t *testing.T) {
	fixture := &rewriteSearchFixture{pageRequests: make([]string, 0, 2)}
	server := httptest.NewServer(http.HandlerFunc(fixture.handleRewriteSearch(t)))
	defer server.Close()
	fixture.baseURL = server.URL

	service := NewService(server.URL + searchPath)
	service.parse = parsePageEcho

	activityCount := 0
	progress := make([]ProgressUpdate, 0, 6)
	prepared, err := service.Prepare(context.Background(), sudoPromptQuery, "sudo prompt change linux", func() {
		activityCount++
	}, func(update ProgressUpdate) {
		progress = append(progress, update)
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	assertBuildPromptResult(t, prepared, server.URL, activityCount, fixture, progress)
}

func TestPrepareFallsBackToSearchSnippetWhenParsingFails(t *testing.T) {
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

	prepared, err := service.Prepare(context.Background(), "consulta", "consulta", nil, nil)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !strings.Contains(prepared.Prompt, "search snippet") {
		t.Fatalf("prompt missing fallback snippet: %q", prepared.Prompt)
	}
	if !strings.Contains(prepared.Prompt, server.URL+"/article") {
		t.Fatalf("prompt missing source URL: %q", prepared.Prompt)
	}
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
			f.pageRequests = append(f.pageRequests, r.URL.Path)
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
	if len(fixture.pageRequests) != 2 {
		t.Fatalf("page requests = %d, want 2", len(fixture.pageRequests))
	}
	if prepared.ApproxTokens <= 0 {
		t.Fatalf("approx tokens = %d, want a positive value", prepared.ApproxTokens)
	}
	if len(prepared.Documents) != 2 {
		t.Fatalf("prepared documents len = %d, want 2", len(prepared.Documents))
	}
	assertPromptContains(t, prepared.Prompt, serverURL)
	assertProgressUpdates(t, progress, serverURL)
}

func assertPromptContains(t *testing.T, prompt string, serverURL string) {
	t.Helper()
	if !strings.Contains(prompt, "Consulta original: como cambiar el prompt de sudo") {
		t.Fatalf("prompt missing original query: %q", prompt)
	}
	if !strings.Contains(prompt, "Query usada para la busqueda: sudo prompt change linux") {
		t.Fatalf("prompt missing rewritten search query context: %q", prompt)
	}
	if !strings.Contains(prompt, "System Role:") {
		t.Fatalf("prompt missing system role section: %q", prompt)
	}
	if !strings.Contains(prompt, "Fidelidad Absoluta") {
		t.Fatalf("prompt missing fidelity rule: %q", prompt)
	}
	if !strings.Contains(prompt, "Fuentes Consultadas") {
		t.Fatalf("prompt missing consulted sources section: %q", prompt)
	}
	if !strings.Contains(prompt, "- [1] "+serverURL+"/b") {
		t.Fatalf("prompt missing numbered top result URL: %q", prompt)
	}
	if !strings.Contains(prompt, "- [2] "+serverURL+"/a") {
		t.Fatalf("prompt missing numbered second result URL: %q", prompt)
	}
	if !strings.Contains(prompt, "Esto es una prueba [1]") {
		t.Fatalf("prompt missing explicit footer example: %q", prompt)
	}
	if !strings.Contains(prompt, "- [1] https://example.com") {
		t.Fatalf("prompt missing bullet list example: %q", prompt)
	}
	if !strings.Contains(prompt, serverURL+"/b") {
		t.Fatalf("prompt missing top result URL: %q", prompt)
	}
	if !strings.Contains(prompt, "content page/b") {
		t.Fatalf("prompt missing parsed page content: %q", prompt)
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
