package profiler

import "time"

type LogRecord struct {
	ID         int64
	RunID      string
	Command    string
	StepName   string
	DurationMS int64
	TokensIn   int
	TokensOut  int
	TPS        float64
	ModelName  string
	CommitHash string
	Metadata   map[string]any
	Timestamp  time.Time
}

type Span interface {
	SetModel(name string)
	SetTokens(tokensIn int, tokensOut int)
	SetTPS(tps float64)
	AddMetadata(metadata map[string]any)
	End()
}

type Tracker interface {
	Enabled() bool
	RunID() string
	StartSpan(command string, stepName string, metadata map[string]any) Span
	CurrentRun(command string) []LogRecord
	Comparison(command string, previousRuns int) (ComparisonReport, error)
	Close() error
}

type StepStats struct {
	StepName         string
	DurationMS       float64
	TokensIn         float64
	TokensOut        float64
	TPS              float64
	Samples          int
	CurrentDuration  int64
	CurrentTokensIn  int
	CurrentTokensOut int
	CurrentTPS       float64
}

type ComparisonReport struct {
	Command     string
	CurrentRun  string
	CurrentTime time.Time
	Steps       []StepStats
}
