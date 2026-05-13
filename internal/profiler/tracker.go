package profiler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const sqliteFileName = "performance_logs.sqlite"

type sqliteTracker struct {
	db         *sql.DB
	runID      string
	commitHash string

	mu      sync.Mutex
	records []LogRecord
}

type sqliteSpan struct {
	tracker   *sqliteTracker
	command   string
	stepName  string
	metadata  map[string]any
	modelName string
	tokensIn  int
	tokensOut int
	tps       float64
	start     time.Time
	ended     bool
}

type noopTracker struct{}

type noopSpan struct{}

func Disabled() Tracker { return noopTracker{} }

func New(configPath string, enabled bool) (Tracker, error) {
	if !enabled {
		return noopTracker{}, nil
	}

	dbPath, err := resolveDBPath(configPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create profiler dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite profiler db: %w", err)
	}
	if err := initializeSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	tracker := &sqliteTracker{
		db:         db,
		runID:      newRunID(),
		commitHash: resolveCommitHash(),
		records:    make([]LogRecord, 0, 8),
	}
	return tracker, nil
}

func resolveDBPath(configPath string) (string, error) {
	trimmed := strings.TrimSpace(configPath)
	if trimmed != "" {
		return filepath.Join(filepath.Dir(trimmed), sqliteFileName), nil
	}
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(configRoot, "sparkle-cli", sqliteFileName), nil
}

func initializeSchema(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("sqlite db is nil")
	}
	const schema = `
CREATE TABLE IF NOT EXISTS performance_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  command TEXT NOT NULL,
  step_name TEXT NOT NULL,
  duration_ms INTEGER NOT NULL,
  tokens_in INTEGER NOT NULL DEFAULT 0,
  tokens_out INTEGER NOT NULL DEFAULT 0,
  tps REAL NOT NULL DEFAULT 0,
  model_name TEXT NOT NULL DEFAULT '',
  commit_hash TEXT NOT NULL DEFAULT '',
  metadata_json TEXT NOT NULL DEFAULT '{}',
  timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_performance_logs_command_time ON performance_logs(command, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_performance_logs_run ON performance_logs(run_id);
`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("initialize sqlite schema: %w", err)
	}
	return nil
}

func (t *sqliteTracker) Enabled() bool { return t != nil && t.db != nil }

func (t *sqliteTracker) RunID() string {
	if t == nil {
		return ""
	}
	return t.runID
}

func (t noopTracker) Enabled() bool { return false }
func (t noopTracker) RunID() string { return "" }

func (t *sqliteTracker) StartSpan(command string, stepName string, metadata map[string]any) Span {
	if t == nil || t.db == nil {
		return noopSpan{}
	}
	return &sqliteSpan{
		tracker:  t,
		command:  strings.TrimSpace(command),
		stepName: strings.TrimSpace(stepName),
		metadata: cloneMetadata(metadata),
		start:    time.Now().UTC(),
	}
}

func (t noopTracker) StartSpan(command string, stepName string, metadata map[string]any) Span {
	return noopSpan{}
}

func (s *sqliteSpan) SetModel(name string) {
	s.modelName = strings.TrimSpace(name)
}

func (s *sqliteSpan) SetTokens(tokensIn int, tokensOut int) {
	s.tokensIn = max(tokensIn, 0)
	s.tokensOut = max(tokensOut, 0)
}

func (s *sqliteSpan) SetTPS(tps float64) {
	if tps < 0 {
		tps = 0
	}
	s.tps = tps
}

func (s *sqliteSpan) AddMetadata(metadata map[string]any) {
	for key, value := range metadata {
		s.metadata[key] = value
	}
}

func (s *sqliteSpan) End() {
	if s == nil || s.tracker == nil || s.ended {
		return
	}
	s.ended = true

	duration := time.Since(s.start)
	if duration < 0 {
		duration = 0
	}
	now := time.Now().UTC()

	if s.command == "" {
		s.command = "unknown"
	}
	if s.stepName == "" {
		s.stepName = "unknown"
	}
	if s.metadata == nil {
		s.metadata = map[string]any{}
	}
	s.metadata["run_id"] = s.tracker.runID

	payload, _ := json.Marshal(s.metadata)
	record := LogRecord{
		RunID:      s.tracker.runID,
		Command:    s.command,
		StepName:   s.stepName,
		DurationMS: duration.Milliseconds(),
		TokensIn:   s.tokensIn,
		TokensOut:  s.tokensOut,
		TPS:        s.tps,
		ModelName:  s.modelName,
		CommitHash: s.tracker.commitHash,
		Metadata:   cloneMetadata(s.metadata),
		Timestamp:  now,
	}

	_, err := s.tracker.db.Exec(
		`INSERT INTO performance_logs(run_id, command, step_name, duration_ms, tokens_in, tokens_out, tps, model_name, commit_hash, metadata_json, timestamp)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.RunID,
		record.Command,
		record.StepName,
		record.DurationMS,
		record.TokensIn,
		record.TokensOut,
		record.TPS,
		record.ModelName,
		record.CommitHash,
		string(payload),
		record.Timestamp,
	)
	if err != nil {
		return
	}

	s.tracker.mu.Lock()
	s.tracker.records = append(s.tracker.records, record)
	s.tracker.mu.Unlock()
}

func (s noopSpan) SetModel(name string)                  {}
func (s noopSpan) SetTokens(tokensIn int, tokensOut int) {}
func (s noopSpan) SetTPS(tps float64)                    {}
func (s noopSpan) AddMetadata(metadata map[string]any)   {}
func (s noopSpan) End()                                  {}

func (t *sqliteTracker) CurrentRun(command string) []LogRecord {
	if t == nil {
		return nil
	}
	trimmed := strings.TrimSpace(command)
	t.mu.Lock()
	defer t.mu.Unlock()
	rows := make([]LogRecord, 0, len(t.records))
	for _, record := range t.records {
		if trimmed != "" && !strings.EqualFold(record.Command, trimmed) {
			continue
		}
		rows = append(rows, record)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Timestamp.Before(rows[j].Timestamp) })
	return rows
}

func (t noopTracker) CurrentRun(command string) []LogRecord { return nil }

func (t *sqliteTracker) Comparison(command string, previousRuns int) (ComparisonReport, error) {
	if t == nil || t.db == nil {
		return ComparisonReport{}, nil
	}
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		trimmed = "search"
	}
	if previousRuns <= 0 {
		previousRuns = 10
	}

	runIDs, err := t.listRecentRuns(trimmed, previousRuns+1)
	if err != nil {
		return ComparisonReport{}, err
	}
	if len(runIDs) == 0 {
		return ComparisonReport{Command: trimmed}, nil
	}
	currentRun := runIDs[0]
	current, err := t.loadRunSteps(currentRun, trimmed)
	if err != nil {
		return ComparisonReport{}, err
	}
	currentByStep := map[string]LogRecord{}
	for _, row := range current {
		currentByStep[row.StepName] = row
	}

	previous := runIDs[1:]
	avgByStep := map[string]StepStats{}
	if len(previous) > 0 {
		for _, runID := range previous {
			rows, loadErr := t.loadRunSteps(runID, trimmed)
			if loadErr != nil {
				return ComparisonReport{}, loadErr
			}
			for _, row := range rows {
				stat := avgByStep[row.StepName]
				stat.StepName = row.StepName
				stat.DurationMS += float64(row.DurationMS)
				stat.TokensIn += float64(row.TokensIn)
				stat.TokensOut += float64(row.TokensOut)
				stat.TPS += row.TPS
				stat.Samples++
				avgByStep[row.StepName] = stat
			}
		}
	}

	stepNames := make([]string, 0, len(currentByStep)+len(avgByStep))
	seen := map[string]struct{}{}
	for name := range currentByStep {
		seen[name] = struct{}{}
		stepNames = append(stepNames, name)
	}
	for name := range avgByStep {
		if _, ok := seen[name]; ok {
			continue
		}
		stepNames = append(stepNames, name)
	}
	sort.Strings(stepNames)

	report := ComparisonReport{
		Command:    trimmed,
		CurrentRun: currentRun,
		Steps:      make([]StepStats, 0, len(stepNames)),
	}
	if len(current) > 0 {
		report.CurrentTime = current[0].Timestamp
	}
	for _, name := range stepNames {
		avg := avgByStep[name]
		if avg.Samples > 0 {
			div := float64(avg.Samples)
			avg.DurationMS = avg.DurationMS / div
			avg.TokensIn = avg.TokensIn / div
			avg.TokensOut = avg.TokensOut / div
			avg.TPS = avg.TPS / div
		}
		if currentRecord, ok := currentByStep[name]; ok {
			avg.CurrentDuration = currentRecord.DurationMS
			avg.CurrentTokensIn = currentRecord.TokensIn
			avg.CurrentTokensOut = currentRecord.TokensOut
			avg.CurrentTPS = currentRecord.TPS
		}
		report.Steps = append(report.Steps, avg)
	}
	return report, nil
}

func (t noopTracker) Comparison(command string, previousRuns int) (ComparisonReport, error) {
	return ComparisonReport{}, nil
}

func (t *sqliteTracker) listRecentRuns(command string, limit int) ([]string, error) {
	rows, err := t.db.Query(
		`SELECT run_id, MAX(timestamp) AS ts
         FROM performance_logs
         WHERE command = ?
         GROUP BY run_id
         ORDER BY ts DESC
         LIMIT ?`,
		command,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list recent profiler runs: %w", err)
	}
	defer rows.Close()

	runIDs := make([]string, 0, limit)
	for rows.Next() {
		var runID string
		var ignored any
		if scanErr := rows.Scan(&runID, &ignored); scanErr != nil {
			return nil, fmt.Errorf("scan profiler run ids: %w", scanErr)
		}
		runIDs = append(runIDs, runID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate profiler run ids: %w", err)
	}
	return runIDs, nil
}

func (t *sqliteTracker) loadRunSteps(runID string, command string) ([]LogRecord, error) {
	rows, err := t.db.Query(
		`SELECT id, run_id, command, step_name, duration_ms, tokens_in, tokens_out, tps, model_name, commit_hash, metadata_json, timestamp
         FROM performance_logs
         WHERE run_id = ? AND command = ?
         ORDER BY timestamp ASC, id ASC`,
		runID,
		command,
	)
	if err != nil {
		return nil, fmt.Errorf("load profiler run steps: %w", err)
	}
	defer rows.Close()

	result := make([]LogRecord, 0, 8)
	for rows.Next() {
		var record LogRecord
		var metadataJSON string
		if scanErr := rows.Scan(
			&record.ID,
			&record.RunID,
			&record.Command,
			&record.StepName,
			&record.DurationMS,
			&record.TokensIn,
			&record.TokensOut,
			&record.TPS,
			&record.ModelName,
			&record.CommitHash,
			&metadataJSON,
			&record.Timestamp,
		); scanErr != nil {
			return nil, fmt.Errorf("scan profiler row: %w", scanErr)
		}
		record.Metadata = map[string]any{}
		if strings.TrimSpace(metadataJSON) != "" {
			_ = json.Unmarshal([]byte(metadataJSON), &record.Metadata)
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate profiler rows: %w", err)
	}
	return result, nil
}

func (t *sqliteTracker) Close() error {
	if t == nil || t.db == nil {
		return nil
	}
	return t.db.Close()
}

func (t noopTracker) Close() error { return nil }

func cloneMetadata(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func newRunID() string {
	now := time.Now().UTC().Format("20060102T150405.000000000Z")
	return fmt.Sprintf("%s-%d", now, os.Getpid())
}

func resolveCommitHash() string {
	if value := strings.TrimSpace(os.Getenv("SPARKLE_COMMIT_HASH")); value != "" {
		return value
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil {
		return "unknown"
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			if value := strings.TrimSpace(setting.Value); value != "" {
				return value
			}
		}
	}
	return "unknown"
}
