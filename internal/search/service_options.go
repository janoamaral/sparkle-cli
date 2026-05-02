package search

import (
	"context"

	"github.com/logico/sparkle-cli/internal/profiler"
)

type EmbeddingProvider interface {
	EmbedWithModel(ctx context.Context, model string, input []string) ([][]float32, error)
}

type DomainReputationProvider interface {
	AdjustScore(ctx context.Context, rawURL string, baseScore float64) (float64, error)
}

type QdrantConfig struct {
	Enabled        bool
	Host           string
	Port           int
	APIKey         string
	UseTLS         bool
	Collection     string
	ScoreThreshold float64
	TTLHours       int
	PoolSize       int
}

type Option func(*Service)

func WithProfiler(tracker profiler.Tracker) Option {
	return func(s *Service) {
		s.tracker = tracker
	}
}

func WithEmbedder(provider EmbeddingProvider, model string) Option {
	return func(s *Service) {
		s.embedder = provider
		s.embeddingModel = model
	}
}

func WithQdrantCache(cfg QdrantConfig) Option {
	return func(s *Service) {
		store := newQdrantSemanticCache(cfg)
		if store != nil {
			s.cache = store
		}
	}
}

func WithDomainReputation(provider DomainReputationProvider) Option {
	return func(s *Service) {
		s.domainReputation = provider
	}
}

func withSemanticCacheStore(store semanticCacheStore) Option {
	return func(s *Service) {
		s.cache = store
	}
}

func CacheLookupKey() string {
	return cacheLookupKey
}

func CachePersistKey() string {
	return cachePersistKey
}
