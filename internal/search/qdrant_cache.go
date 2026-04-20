package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	qdrantapi "github.com/qdrant/go-client/qdrant"
)

const (
	maxCacheLookupResults   = 15
	cacheChunkTokenSize     = 500
	cacheChunkTokenOverlap  = 80
	cacheChunkContentMaxLen = 4000
	cacheLookupKey          = "cache-lookup"
	cachePersistKey         = "cache-persist"
	cacheQueryMinRerank     = 1.25
	cacheIngestTimeout      = 2 * time.Minute
)

var qdrantClientInitMu sync.Mutex

type semanticCacheStore interface {
	Lookup(ctx context.Context, vector []float32, now time.Time) ([]cachedChunk, error)
	Ingest(ctx context.Context, points []cachePoint) error
	Close() error
}

type cachedChunk struct {
	Title         string
	URL           string
	Content       string
	OriginalQuery string
	Hash          string
	Timestamp     time.Time
	Score         float64
}

type cachePoint struct {
	ID            string
	Title         string
	URL           string
	Content       string
	OriginalQuery string
	Hash          string
	TimestampUnix int64
	Vector        []float32
}

type qdrantSemanticCache struct {
	cfg               QdrantConfig
	mu                sync.Mutex
	client            *qdrantapi.Client
	collectionChecked bool
	collectionExists  bool
}

func newQdrantSemanticCache(cfg QdrantConfig) *qdrantSemanticCache {
	if !cfg.Enabled {
		return nil
	}
	return &qdrantSemanticCache{cfg: cfg}
}

func (c *qdrantSemanticCache) Lookup(ctx context.Context, vector []float32, now time.Time) ([]cachedChunk, error) {
	if len(vector) == 0 {
		return nil, nil
	}
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}
	exists, err := c.collectionAvailable(ctx, client)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	limit := uint64(maxCacheLookupResults)
	threshold := float32(c.cfg.ScoreThreshold)
	hits, err := client.Query(ctx, &qdrantapi.QueryPoints{
		CollectionName: c.cfg.Collection,
		Query:          qdrantapi.NewQuery(vector...),
		Limit:          &limit,
		ScoreThreshold: &threshold,
		WithPayload:    qdrantapi.NewWithPayload(true),
	})
	if err != nil {
		return nil, err
	}

	results := make([]cachedChunk, 0, len(hits))
	for _, hit := range hits {
		if hit == nil {
			continue
		}
		payload := hit.GetPayload()
		cachedAt := payloadInt64(payload, "timestamp")
		if cachedAt == 0 {
			continue
		}
		timestamp := time.Unix(cachedAt, 0).UTC()
		if now.Sub(timestamp) > time.Duration(c.cfg.TTLHours)*time.Hour {
			continue
		}
		content := clampText(payloadString(payload, "content"), cacheChunkContentMaxLen)
		if content == "" {
			continue
		}
		results = append(results, cachedChunk{
			Title:         safeTitle(payloadString(payload, "title")),
			URL:           strings.TrimSpace(payloadString(payload, "url")),
			Content:       content,
			OriginalQuery: strings.TrimSpace(payloadString(payload, "original_query")),
			Hash:          strings.TrimSpace(payloadString(payload, "hash")),
			Timestamp:     timestamp,
			Score:         float64(hit.GetScore()),
		})
	}

	return results, nil
}

func (c *qdrantSemanticCache) Ingest(ctx context.Context, points []cachePoint) error {
	if len(points) == 0 {
		return nil
	}
	client, err := c.getClient()
	if err != nil {
		return err
	}
	if err := c.ensureCollection(ctx, client, len(points[0].Vector)); err != nil {
		return err
	}

	wait := true
	upsertPoints := make([]*qdrantapi.PointStruct, 0, len(points))
	for _, point := range points {
		if len(point.Vector) == 0 {
			continue
		}
		upsertPoints = append(upsertPoints, &qdrantapi.PointStruct{
			Id:      qdrantapi.NewID(cachePointUUID(point.ID)),
			Vectors: qdrantapi.NewVectors(point.Vector...),
			Payload: qdrantapi.NewValueMap(map[string]any{
				"title":          point.Title,
				"url":            point.URL,
				"content":        point.Content,
				"timestamp":      point.TimestampUnix,
				"original_query": point.OriginalQuery,
				"hash":           point.Hash,
			}),
		})
	}
	if len(upsertPoints) == 0 {
		return nil
	}

	_, err = client.Upsert(ctx, &qdrantapi.UpsertPoints{
		CollectionName: c.cfg.Collection,
		Wait:           &wait,
		Points:         upsertPoints,
	})
	return err
}

func (c *qdrantSemanticCache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client == nil {
		return nil
	}
	err := c.client.Close()
	c.client = nil
	c.collectionChecked = false
	c.collectionExists = false
	return err
}

func (c *qdrantSemanticCache) getClient() (*qdrantapi.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != nil {
		return c.client, nil
	}
	poolSize := uint(c.cfg.PoolSize)
	client, err := newSilentQdrantClient(&qdrantapi.Config{
		Host:     strings.TrimSpace(c.cfg.Host),
		Port:     c.cfg.Port,
		APIKey:   strings.TrimSpace(c.cfg.APIKey),
		UseTLS:   c.cfg.UseTLS,
		PoolSize: poolSize,
	})
	if err != nil {
		return nil, err
	}
	c.client = client
	return c.client, nil
}

func newSilentQdrantClient(cfg *qdrantapi.Config) (*qdrantapi.Client, error) {
	qdrantClientInitMu.Lock()
	defer qdrantClientInitMu.Unlock()

	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(previousLogger)

	return qdrantapi.NewClient(cfg)
}

func (c *qdrantSemanticCache) ensureCollection(ctx context.Context, client *qdrantapi.Client, vectorSize int) error {
	exists, err := c.collectionAvailable(ctx, client)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if err := client.CreateCollection(ctx, &qdrantapi.CreateCollection{
		CollectionName: c.cfg.Collection,
		VectorsConfig: qdrantapi.NewVectorsConfig(&qdrantapi.VectorParams{
			Size:     uint64(vectorSize),
			Distance: qdrantapi.Distance_Cosine,
		}),
	}); err != nil {
		return err
	}
	c.mu.Lock()
	c.collectionChecked = true
	c.collectionExists = true
	c.mu.Unlock()
	return nil
}

func (c *qdrantSemanticCache) collectionAvailable(ctx context.Context, client *qdrantapi.Client) (bool, error) {
	c.mu.Lock()
	if c.collectionChecked {
		exists := c.collectionExists
		c.mu.Unlock()
		return exists, nil
	}
	c.mu.Unlock()

	exists, err := client.CollectionExists(ctx, c.cfg.Collection)
	if err != nil {
		return false, err
	}

	c.mu.Lock()
	c.collectionChecked = true
	c.collectionExists = exists
	c.mu.Unlock()
	return exists, nil
}

func payloadString(payload map[string]*qdrantapi.Value, key string) string {
	value := payload[key]
	if value == nil {
		return ""
	}
	switch current := value.Kind.(type) {
	case *qdrantapi.Value_StringValue:
		return current.StringValue
	case *qdrantapi.Value_IntegerValue:
		return strconv.FormatInt(current.IntegerValue, 10)
	case *qdrantapi.Value_DoubleValue:
		return strconv.FormatFloat(current.DoubleValue, 'f', -1, 64)
	default:
		return ""
	}
}

func payloadInt64(payload map[string]*qdrantapi.Value, key string) int64 {
	value := payload[key]
	if value == nil {
		return 0
	}
	switch current := value.Kind.(type) {
	case *qdrantapi.Value_IntegerValue:
		return current.IntegerValue
	case *qdrantapi.Value_StringValue:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(current.StringValue), 10, 64)
		return parsed
	default:
		return 0
	}
}

func rerankCachedChunks(query string, cached []cachedChunk) []Document {
	if len(cached) == 0 {
		return nil
	}
	queryTerms := rankingTerms(query)
	normalizedQuery := normalizeRankingText(query)
	if len(queryTerms) == 0 {
		return nil
	}

	type rankedChunk struct {
		chunk cachedChunk
		score float64
		index int
	}
	ranked := make([]rankedChunk, 0, len(cached))
	for index, current := range cached {
		document := Document{
			Title:   current.Title,
			URL:     current.URL,
			Excerpt: current.OriginalQuery,
			Content: current.Content,
			Score:   current.Score,
			Index:   index + 1,
		}
		chunk := Chunk{Text: current.Content}
		lexicalScore := scoreChunk(normalizedQuery, queryTerms, document, chunk)
		totalScore := lexicalScore + current.Score*2
		if totalScore < cacheQueryMinRerank {
			continue
		}
		ranked = append(ranked, rankedChunk{chunk: current, score: totalScore, index: index})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].index < ranked[j].index
		}
		return ranked[i].score > ranked[j].score
	})

	documents := make([]Document, 0, len(ranked))
	for index, current := range ranked {
		documents = append(documents, Document{
			Title:   safeTitle(current.chunk.Title),
			URL:     current.chunk.URL,
			Excerpt: current.chunk.OriginalQuery,
			Content: current.chunk.Content,
			Score:   current.score,
			Index:   index + 1,
		})
	}

	return documents
}

func buildCachePoints(query string, vectors [][]float32, chunks []cachePointSeed) []cachePoint {
	points := make([]cachePoint, 0, len(chunks))
	for index, current := range chunks {
		if index >= len(vectors) || len(vectors[index]) == 0 {
			continue
		}
		points = append(points, cachePoint{
			ID:            current.ID,
			Title:         current.Title,
			URL:           current.URL,
			Content:       current.Content,
			OriginalQuery: query,
			Hash:          current.Hash,
			TimestampUnix: current.TimestampUnix,
			Vector:        vectors[index],
		})
	}
	return points
}

type cachePointSeed struct {
	ID            string
	Title         string
	URL           string
	Content       string
	Hash          string
	TimestampUnix int64
}

func buildCachePointSeeds(now time.Time, documents []Document) []cachePointSeed {
	seeds := make([]cachePointSeed, 0, len(documents)*2)
	seen := make(map[string]struct{}, len(documents)*2)
	for _, document := range documents {
		for _, chunk := range splitIntoSemanticCacheChunks(document.Content) {
			trimmed := clampText(chunk, cacheChunkContentMaxLen)
			if trimmed == "" {
				continue
			}
			hash := hashChunk(trimmed)
			if _, ok := seen[hash]; ok {
				continue
			}
			seen[hash] = struct{}{}
			seeds = append(seeds, cachePointSeed{
				ID:            hash,
				Title:         safeTitle(document.Title),
				URL:           strings.TrimSpace(document.URL),
				Content:       trimmed,
				Hash:          hash,
				TimestampUnix: now.Unix(),
			})
		}
	}
	return seeds
}

func splitIntoSemanticCacheChunks(value string) []string {
	words := strings.Fields(strings.TrimSpace(value))
	if len(words) == 0 {
		return nil
	}
	chunks := make([]string, 0, max(1, len(words)/80))
	current := make([]string, 0, 96)
	currentTokens := 0
	for _, word := range words {
		wordTokens := max(1, ApproximateTokenCount(word))
		if currentTokens > 0 && currentTokens+wordTokens > cacheChunkTokenSize {
			chunks = append(chunks, strings.Join(current, " "))
			current = trailingWordsForTokenBudget(current, cacheChunkTokenOverlap)
			currentTokens = ApproximateTokenCount(strings.Join(current, " "))
		}
		current = append(current, word)
		currentTokens += wordTokens
	}
	if len(current) > 0 {
		chunks = append(chunks, strings.Join(current, " "))
	}
	return chunks
}

func trailingWordsForTokenBudget(words []string, budget int) []string {
	if len(words) == 0 || budget <= 0 {
		return nil
	}
	collected := make([]string, 0, len(words))
	tokens := 0
	for index := len(words) - 1; index >= 0; index-- {
		wordTokens := max(1, ApproximateTokenCount(words[index]))
		if tokens > 0 && tokens+wordTokens > budget {
			break
		}
		collected = append(collected, words[index])
		tokens += wordTokens
	}
	for left, right := 0, len(collected)-1; left < right; left, right = left+1, right-1 {
		collected[left], collected[right] = collected[right], collected[left]
	}
	return collected
}

func hashChunk(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func cachePointUUID(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if len(trimmed) < 32 || !isHexString(trimmed) {
		trimmed = hashChunk(trimmed)
	}
	base := []byte(trimmed[:32])
	base[12] = '5'
	base[16] = 'a'
	return fmt.Sprintf("%s-%s-%s-%s-%s", base[:8], base[8:12], base[12:16], base[16:20], base[20:32])
}

func isHexString(value string) bool {
	for _, current := range value {
		if (current < '0' || current > '9') && (current < 'a' || current > 'f') {
			return false
		}
	}
	return value != ""
}

func buildPreparedPrompt(query string, searchQuery string, documents []Document) PreparedPrompt {
	prompt := buildSummaryPrompt(query, searchQuery, documents)
	return PreparedPrompt{
		Query:        query,
		Prompt:       prompt,
		ApproxTokens: ApproximateTokenCount(prompt),
		Documents:    append([]Document(nil), documents...),
	}
}

func describeCacheLookup(documents []Document) string {
	if len(documents) == 0 {
		return "Cache semantica sin hits frescos"
	}
	return fmt.Sprintf("Cache semantica reutilizada: %d fragmentos", len(documents))
}
