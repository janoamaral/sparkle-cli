package feedback

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	qdrantapi "github.com/qdrant/go-client/qdrant"

	_ "modernc.org/sqlite"
)

const (
	ratingNeutral  = "neutral"
	ratingPositive = "positive"
	ratingNegative = "negative"

	defaultDBFileName           = "feedback.db"
	defaultExperienceCollection = "search_experiences"
	defaultDedupThreshold       = 0.97
	defaultSimilarityThreshold  = 0.8
	defaultHalfLifeDays         = 30
	defaultPositiveWeight       = 0.35
	defaultNegativeWeight       = 0.9
	interactionIndexTimeout     = 10 * time.Second
)

type Vote int

const (
	VoteNeutral Vote = iota
	VotePositive
	VoteNegative
)

type RewriteExample struct {
	OriginalQuery string
	Queries       []string
}

type EmbeddingProvider interface {
	EmbedWithModel(ctx context.Context, model string, input []string) ([][]float32, error)
}

type QdrantConfig struct {
	Enabled              bool
	Host                 string
	Port                 int
	APIKey               string
	UseTLS               bool
	PoolSize             int
	Collection           string
	DedupScoreThreshold  float64
	SearchScoreThreshold float64
}

type Options struct {
	ConfigPath     string
	Embedder       EmbeddingProvider
	EmbeddingModel string
	Qdrant         QdrantConfig
	HalfLifeDays   int
	PositiveWeight float64
	NegativeWeight float64
}

type Store struct {
	db             *sql.DB
	embedder       EmbeddingProvider
	embeddingModel string
	halfLife       time.Duration
	beta           float64
	gamma          float64

	qdrant QdrantConfig
	qmu    sync.Mutex
	qcli   *qdrantapi.Client
}

func Open(options Options) (*Store, error) {
	dbPath, err := resolveDBPath(options.ConfigPath)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open feedback sqlite database %s: %w", dbPath, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping feedback sqlite database %s: %w", dbPath, err)
	}

	store := &Store{
		db:             db,
		embedder:       options.Embedder,
		embeddingModel: strings.TrimSpace(options.EmbeddingModel),
		halfLife:       time.Duration(nonZero(options.HalfLifeDays, defaultHalfLifeDays)) * 24 * time.Hour,
		beta:           positiveWeight(options.PositiveWeight),
		gamma:          negativeWeight(options.NegativeWeight),
		qdrant:         normalizeQdrantConfig(options.Qdrant),
	}

	if err := store.ensureSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func resolveDBPath(configPath string) (string, error) {
	baseDir := ""
	trimmed := strings.TrimSpace(configPath)
	if trimmed != "" {
		baseDir = filepath.Dir(trimmed)
	} else {
		configRoot, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("resolve user config dir for feedback database: %w", err)
		}
		baseDir = filepath.Join(configRoot, "sparkle-cli")
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", fmt.Errorf("create feedback database directory %s: %w", baseDir, err)
	}
	return filepath.Join(baseDir, defaultDBFileName), nil
}

func normalizeQdrantConfig(cfg QdrantConfig) QdrantConfig {
	cfg.Collection = strings.TrimSpace(cfg.Collection)
	if cfg.Collection == "" {
		cfg.Collection = defaultExperienceCollection
	}
	if cfg.DedupScoreThreshold <= 0 || cfg.DedupScoreThreshold > 1 {
		cfg.DedupScoreThreshold = defaultDedupThreshold
	}
	if cfg.SearchScoreThreshold <= 0 || cfg.SearchScoreThreshold > 1 {
		cfg.SearchScoreThreshold = defaultSimilarityThreshold
	}
	if cfg.PoolSize <= 0 {
		cfg.PoolSize = 3
	}
	return cfg
}

func nonZero(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func positiveWeight(value float64) float64 {
	if value <= 0 {
		return defaultPositiveWeight
	}
	return value
}

func negativeWeight(value float64) float64 {
	if value <= 0 {
		return defaultNegativeWeight
	}
	if value <= defaultPositiveWeight {
		return defaultNegativeWeight
	}
	return value
}

func (s *Store) ensureSchema() error {
	if s == nil || s.db == nil {
		return errors.New("feedback store is not initialized")
	}

	statements := []string{
		`CREATE TABLE IF NOT EXISTS search_interactions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			original_query TEXT NOT NULL,
			qdrant_point_id TEXT,
			rating TEXT NOT NULL DEFAULT 'neutral',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS search_query_traces (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			interaction_id INTEGER NOT NULL,
			query_order INTEGER NOT NULL,
			query_text TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			FOREIGN KEY(interaction_id) REFERENCES search_interactions(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS interaction_domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			interaction_id INTEGER NOT NULL,
			domain TEXT NOT NULL,
			source_url TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			FOREIGN KEY(interaction_id) REFERENCES search_interactions(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS feedback_weights (
			domain TEXT PRIMARY KEY,
			thumbs_up REAL NOT NULL DEFAULT 0,
			thumbs_down REAL NOT NULL DEFAULT 0,
			last_decay_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_search_interactions_rating_updated_at ON search_interactions(rating, updated_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_search_query_traces_interaction_order ON search_query_traces(interaction_id, query_order);`,
		`CREATE INDEX IF NOT EXISTS idx_interaction_domains_interaction ON interaction_domains(interaction_id);`,
		`CREATE INDEX IF NOT EXISTS idx_interaction_domains_domain ON interaction_domains(domain);`,
	}

	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("initialize feedback schema: %w", err)
		}
	}

	return nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}

	var closeErr error
	s.qmu.Lock()
	if s.qcli != nil {
		closeErr = s.qcli.Close()
		s.qcli = nil
	}
	s.qmu.Unlock()

	if s.db != nil {
		dbErr := s.db.Close()
		s.db = nil
		if closeErr == nil {
			closeErr = dbErr
		}
	}

	return closeErr
}

func (s *Store) CreateInteraction(ctx context.Context, originalQuery string, rewrittenQueries []string, sourceURLs []string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("feedback store is not initialized")
	}

	trimmedOriginal := strings.TrimSpace(originalQuery)
	if trimmedOriginal == "" {
		return 0, errors.New("original query is empty")
	}

	queries := compactQueries(rewrittenQueries)
	if len(queries) == 0 {
		queries = []string{trimmedOriginal}
	}

	nowUnix := time.Now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	result, err := tx.ExecContext(ctx, `INSERT INTO search_interactions (original_query, qdrant_point_id, rating, created_at, updated_at) VALUES (?, '', ?, ?, ?)`, trimmedOriginal, ratingNeutral, nowUnix, nowUnix)
	if err != nil {
		return 0, err
	}

	interactionID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	for index, query := range queries {
		if _, err = tx.ExecContext(ctx, `INSERT INTO search_query_traces (interaction_id, query_order, query_text, created_at) VALUES (?, ?, ?, ?)`, interactionID, index+1, query, nowUnix); err != nil {
			return 0, err
		}
	}

	uniqueDomains := domainsFromURLs(sourceURLs)
	for domain, urls := range uniqueDomains {
		for _, sourceURL := range urls {
			if _, err = tx.ExecContext(ctx, `INSERT INTO interaction_domains (interaction_id, domain, source_url, created_at) VALUES (?, ?, ?, ?)`, interactionID, domain, sourceURL, nowUnix); err != nil {
				return 0, err
			}
		}
	}

	if err = tx.Commit(); err != nil {
		return 0, err
	}

	go s.indexInteraction(interactionID, trimmedOriginal)
	return interactionID, nil
}

func compactQueries(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func domainsFromURLs(urls []string) map[string][]string {
	if len(urls) == 0 {
		return nil
	}
	byDomain := make(map[string][]string, len(urls))
	seen := make(map[string]struct{}, len(urls))
	for _, raw := range urls {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		domain := normalizeDomain(trimmed)
		if domain == "" {
			continue
		}
		dedupeKey := domain + "|" + strings.ToLower(trimmed)
		if _, ok := seen[dedupeKey]; ok {
			continue
		}
		seen[dedupeKey] = struct{}{}
		byDomain[domain] = append(byDomain[domain], trimmed)
	}
	return byDomain
}

func normalizeDomain(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return ""
	}
	return host
}

func (s *Store) ApplyVote(ctx context.Context, interactionID int64, vote Vote) error {
	if s == nil || s.db == nil {
		return errors.New("feedback store is not initialized")
	}
	if interactionID <= 0 {
		return errors.New("interaction id must be greater than zero")
	}

	newRating := voteToRating(vote)
	now := time.Now().UTC()
	nowUnix := now.Unix()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var currentRating string
	if err = tx.QueryRowContext(ctx, `SELECT rating FROM search_interactions WHERE id = ?`, interactionID).Scan(&currentRating); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if currentRating == newRating {
		return tx.Commit()
	}

	if _, err = tx.ExecContext(ctx, `UPDATE search_interactions SET rating = ?, updated_at = ? WHERE id = ?`, newRating, nowUnix, interactionID); err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx, `SELECT domain FROM interaction_domains WHERE interaction_id = ?`, interactionID)
	if err != nil {
		return err
	}
	domains := make([]string, 0, 8)
	for rows.Next() {
		var domain string
		if scanErr := rows.Scan(&domain); scanErr != nil {
			_ = rows.Close()
			return scanErr
		}
		domains = append(domains, strings.TrimSpace(domain))
	}
	if closeErr := rows.Close(); closeErr != nil {
		return closeErr
	}

	oldPositive := ratingToUpWeight(currentRating)
	newPositive := ratingToUpWeight(newRating)
	oldNegative := ratingToDownWeight(currentRating)
	newNegative := ratingToDownWeight(newRating)
	deltaUp := newPositive - oldPositive
	deltaDown := newNegative - oldNegative

	for _, domain := range domains {
		if domain == "" {
			continue
		}
		thumbsUp, thumbsDown, lastDecayAt, found, lookupErr := fetchDomainWeights(ctx, tx, domain)
		if lookupErr != nil {
			return lookupErr
		}
		if !found {
			thumbsUp = 0
			thumbsDown = 0
			lastDecayAt = nowUnix
		}

		decayedUp, decayedDown := s.applyDecay(thumbsUp, thumbsDown, time.Unix(lastDecayAt, 0), now)
		updatedUp := maxFloat(0, decayedUp+deltaUp)
		updatedDown := maxFloat(0, decayedDown+deltaDown)

		if _, err = tx.ExecContext(ctx, `INSERT INTO feedback_weights (domain, thumbs_up, thumbs_down, last_decay_at, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(domain) DO UPDATE SET
				thumbs_up = excluded.thumbs_up,
				thumbs_down = excluded.thumbs_down,
				last_decay_at = excluded.last_decay_at,
				updated_at = excluded.updated_at`, domain, updatedUp, updatedDown, nowUnix, nowUnix); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func fetchDomainWeights(ctx context.Context, tx *sql.Tx, domain string) (float64, float64, int64, bool, error) {
	var thumbsUp float64
	var thumbsDown float64
	var lastDecayAt int64
	err := tx.QueryRowContext(ctx, `SELECT thumbs_up, thumbs_down, last_decay_at FROM feedback_weights WHERE domain = ?`, domain).Scan(&thumbsUp, &thumbsDown, &lastDecayAt)
	if err == nil {
		return thumbsUp, thumbsDown, lastDecayAt, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, false, nil
	}
	return 0, 0, 0, false, err
}

func (s *Store) applyDecay(thumbsUp float64, thumbsDown float64, lastDecay time.Time, now time.Time) (float64, float64) {
	if s == nil || s.halfLife <= 0 || lastDecay.IsZero() || !now.After(lastDecay) {
		return thumbsUp, thumbsDown
	}
	elapsed := now.Sub(lastDecay)
	if elapsed <= 0 {
		return thumbsUp, thumbsDown
	}
	decayLambda := math.Ln2 / s.halfLife.Seconds()
	factor := math.Exp(-decayLambda * elapsed.Seconds())
	return thumbsUp * factor, thumbsDown * factor
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func ratingToUpWeight(rating string) float64 {
	if strings.EqualFold(strings.TrimSpace(rating), ratingPositive) {
		return 1
	}
	return 0
}

func ratingToDownWeight(rating string) float64 {
	if strings.EqualFold(strings.TrimSpace(rating), ratingNegative) {
		return 1
	}
	return 0
}

func voteToRating(vote Vote) string {
	switch vote {
	case VotePositive:
		return ratingPositive
	case VoteNegative:
		return ratingNegative
	default:
		return ratingNeutral
	}
}

func (s *Store) AdjustScore(ctx context.Context, rawURL string, baseScore float64) (float64, error) {
	if s == nil || s.db == nil {
		return baseScore, nil
	}
	domain := normalizeDomain(rawURL)
	if domain == "" {
		return baseScore, nil
	}

	var thumbsUp float64
	var thumbsDown float64
	var lastDecayAt int64
	err := s.db.QueryRowContext(ctx, `SELECT thumbs_up, thumbs_down, last_decay_at FROM feedback_weights WHERE domain = ?`, domain).Scan(&thumbsUp, &thumbsDown, &lastDecayAt)
	if errors.Is(err, sql.ErrNoRows) {
		return baseScore, nil
	}
	if err != nil {
		return baseScore, err
	}

	decayedUp, decayedDown := s.applyDecay(thumbsUp, thumbsDown, time.Unix(lastDecayAt, 0), time.Now().UTC())
	return baseScore + (s.beta * decayedUp) - (s.gamma * decayedDown), nil
}

func (s *Store) PositiveExamples(ctx context.Context, query string, limit int) ([]RewriteExample, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 3
	}

	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		return nil, nil
	}

	interactionIDs := make([]int64, 0, limit)
	if s.qdrant.Enabled && s.embedder != nil {
		vectorCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		vectors, embedErr := s.embedder.EmbedWithModel(vectorCtx, s.embeddingModel, []string{trimmedQuery})
		cancel()
		if embedErr == nil && len(vectors) > 0 && len(vectors[0]) > 0 {
			ids, queryErr := s.querySimilarInteractionIDs(ctx, vectors[0], limit*4)
			if queryErr == nil {
				interactionIDs = append(interactionIDs, ids...)
			}
		}
	}

	if len(interactionIDs) == 0 {
		recent, err := s.fetchRecentPositiveInteractionIDs(ctx, limit)
		if err != nil {
			return nil, err
		}
		interactionIDs = append(interactionIDs, recent...)
	}

	return s.loadPositiveExamples(ctx, interactionIDs, limit)
}

func (s *Store) fetchRecentPositiveInteractionIDs(ctx context.Context, limit int) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM search_interactions WHERE rating = ? ORDER BY updated_at DESC LIMIT ?`, ratingPositive, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	ids := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, scanErr
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) loadPositiveExamples(ctx context.Context, interactionIDs []int64, limit int) ([]RewriteExample, error) {
	if len(interactionIDs) == 0 || limit <= 0 {
		return nil, nil
	}

	examples := make([]RewriteExample, 0, min(limit, len(interactionIDs)))
	seen := make(map[int64]struct{}, len(interactionIDs))
	for _, interactionID := range interactionIDs {
		if _, ok := seen[interactionID]; ok {
			continue
		}
		seen[interactionID] = struct{}{}

		var original string
		var rating string
		err := s.db.QueryRowContext(ctx, `SELECT original_query, rating FROM search_interactions WHERE id = ?`, interactionID).Scan(&original, &rating)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(strings.TrimSpace(rating), ratingPositive) {
			continue
		}

		traces, err := s.loadQueryTraces(ctx, interactionID)
		if err != nil {
			return nil, err
		}
		if len(traces) == 0 {
			continue
		}

		examples = append(examples, RewriteExample{OriginalQuery: strings.TrimSpace(original), Queries: traces})
		if len(examples) >= limit {
			break
		}
	}

	if len(examples) > limit {
		examples = examples[:limit]
	}

	return examples, nil
}

func (s *Store) loadQueryTraces(ctx context.Context, interactionID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT query_text FROM search_query_traces WHERE interaction_id = ? ORDER BY query_order ASC`, interactionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	queries := make([]string, 0, 3)
	for rows.Next() {
		var query string
		if scanErr := rows.Scan(&query); scanErr != nil {
			return nil, scanErr
		}
		trimmed := strings.TrimSpace(query)
		if trimmed != "" {
			queries = append(queries, trimmed)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return queries, nil
}

func (s *Store) indexInteraction(interactionID int64, originalQuery string) {
	if s == nil || !s.qdrant.Enabled || s.embedder == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), interactionIndexTimeout)
	defer cancel()

	vectors, err := s.embedder.EmbedWithModel(ctx, s.embeddingModel, []string{originalQuery})
	if err != nil || len(vectors) == 0 || len(vectors[0]) == 0 {
		return
	}
	vector := vectors[0]

	client, err := s.getQdrantClient()
	if err != nil {
		return
	}
	if err := s.ensureQdrantCollection(ctx, client, len(vector)); err != nil {
		return
	}

	pointID := fmt.Sprintf("experience-%d", interactionID)

	if existingPointID, dedupeErr := s.findDedupePointID(ctx, client, vector); dedupeErr == nil && existingPointID != "" {
		pointID = existingPointID
	}

	payload, payloadErr := qdrantapi.TryValueMap(map[string]any{
		"interaction_id": interactionID,
		"original_query": strings.ToValidUTF8(originalQuery, ""),
		"updated_at":     time.Now().UTC().Unix(),
	})
	if payloadErr != nil {
		return
	}

	wait := true
	_, err = client.Upsert(ctx, &qdrantapi.UpsertPoints{
		CollectionName: s.qdrant.Collection,
		Wait:           &wait,
		Points: []*qdrantapi.PointStruct{{
			Id:      qdrantapi.NewID(cachePointUUID(pointID)),
			Vectors: qdrantapi.NewVectors(vector...),
			Payload: payload,
		}},
	})
	if err != nil {
		return
	}

	_, _ = s.db.ExecContext(context.Background(), `UPDATE search_interactions SET qdrant_point_id = ?, updated_at = ? WHERE id = ?`, pointID, time.Now().UTC().Unix(), interactionID)
}

func (s *Store) querySimilarInteractionIDs(ctx context.Context, vector []float32, limit int) ([]int64, error) {
	if len(vector) == 0 || limit <= 0 {
		return nil, nil
	}
	client, err := s.getQdrantClient()
	if err != nil {
		return nil, err
	}
	if err := s.ensureQdrantCollection(ctx, client, len(vector)); err != nil {
		return nil, err
	}

	queryLimit := uint64(limit)
	threshold := float32(s.qdrant.SearchScoreThreshold)
	hits, err := client.Query(ctx, &qdrantapi.QueryPoints{
		CollectionName: s.qdrant.Collection,
		Query:          qdrantapi.NewQuery(vector...),
		Limit:          &queryLimit,
		ScoreThreshold: &threshold,
		WithPayload:    qdrantapi.NewWithPayload(true),
	})
	if err != nil {
		return nil, err
	}

	ids := make([]int64, 0, len(hits))
	for _, hit := range hits {
		if hit == nil {
			continue
		}
		payload := hit.GetPayload()
		interactionID := payloadInt64(payload, "interaction_id")
		if interactionID <= 0 {
			continue
		}
		ids = append(ids, interactionID)
	}
	return ids, nil
}

func (s *Store) findDedupePointID(ctx context.Context, client *qdrantapi.Client, vector []float32) (string, error) {
	if len(vector) == 0 {
		return "", nil
	}
	limit := uint64(1)
	threshold := float32(s.qdrant.DedupScoreThreshold)
	hits, err := client.Query(ctx, &qdrantapi.QueryPoints{
		CollectionName: s.qdrant.Collection,
		Query:          qdrantapi.NewQuery(vector...),
		Limit:          &limit,
		ScoreThreshold: &threshold,
		WithPayload:    qdrantapi.NewWithPayload(true),
	})
	if err != nil || len(hits) == 0 || hits[0] == nil {
		return "", err
	}
	payload := hits[0].GetPayload()
	pointID := strings.TrimSpace(payloadString(payload, "qdrant_point_id"))
	if pointID != "" {
		return pointID, nil
	}
	interactionID := payloadInt64(payload, "interaction_id")
	if interactionID <= 0 {
		return "", nil
	}
	return fmt.Sprintf("experience-%d", interactionID), nil
}

func (s *Store) ensureQdrantCollection(ctx context.Context, client *qdrantapi.Client, vectorSize int) error {
	exists, err := client.CollectionExists(ctx, s.qdrant.Collection)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return client.CreateCollection(ctx, &qdrantapi.CreateCollection{
		CollectionName: s.qdrant.Collection,
		VectorsConfig: qdrantapi.NewVectorsConfig(&qdrantapi.VectorParams{
			Size:     uint64(vectorSize),
			Distance: qdrantapi.Distance_Cosine,
		}),
	})
}

func (s *Store) getQdrantClient() (*qdrantapi.Client, error) {
	if s == nil || !s.qdrant.Enabled {
		return nil, errors.New("qdrant is disabled")
	}
	s.qmu.Lock()
	defer s.qmu.Unlock()
	if s.qcli != nil {
		return s.qcli, nil
	}
	client, err := qdrantapi.NewClient(&qdrantapi.Config{
		Host:     strings.TrimSpace(s.qdrant.Host),
		Port:     s.qdrant.Port,
		APIKey:   strings.TrimSpace(s.qdrant.APIKey),
		UseTLS:   s.qdrant.UseTLS,
		PoolSize: uint(s.qdrant.PoolSize),
	})
	if err != nil {
		return nil, err
	}
	s.qcli = client
	return s.qcli, nil
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

func cachePointUUID(id string) string {
	source := strings.TrimSpace(id)
	if source == "" {
		source = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
	hash := digest[:32]
	return fmt.Sprintf("%s-%s-%s-%s-%s", hash[0:8], hash[8:12], hash[12:16], hash[16:20], hash[20:32])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
