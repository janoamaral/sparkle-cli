package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/logico/sparkle-cli/internal/config"
	"github.com/logico/sparkle-cli/internal/profiler"
	"github.com/logico/sparkle-cli/internal/search"
	"github.com/logico/sparkle-cli/internal/slash"
)

func RunDirect(cfg config.Config, configPath string, prompt string, mode string, currentDir string, tracker profiler.Tracker) (_ string, err error) {
	directModel := newModelWithTracker(cfg, "", tracker)
	directModel.configPath = configPath
	directModel.currentDir = strings.TrimSpace(currentDir)
	directModel.rebuildSearchRuntime()
	if cfg.Logs {
		logger, loggerErr := newSessionLogger(configPath)
		if loggerErr != nil {
			return "", loggerErr
		}
		directModel.sessionLogger = logger
	}
	defer func() {
		if closeErr := closeModelRuntime(&directModel); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	return runDirectWithModel(&directModel, prompt, mode)
}

func runDirectWithModel(directModel *model, prompt string, mode string) (string, error) {
	if directModel == nil {
		return "", fmt.Errorf("direct mode is not initialized")
	}

	interactionMode, err := normalizeDirectMode(mode)
	if err != nil {
		return "", err
	}
	directModel.mode = interactionMode

	trimmedPrompt := strings.TrimSpace(prompt)
	if trimmedPrompt == "" {
		return "", fmt.Errorf("missing prompt for direct mode")
	}
	if err := validateDirectPrompt(trimmedPrompt); err != nil {
		return "", err
	}

	expansion, err := slash.Resolve(trimmedPrompt, directModel.cfg)
	if err != nil {
		return "", err
	}
	if expansion.Kind == slash.KindConfig {
		return "", fmt.Errorf("slash command %s is not supported in direct mode", slashCommandConfig)
	}

	directModel.logSessionEntry("user_input", trimmedPrompt)

	resolvedPrompt := expansion.Prompt
	requestModel := strings.TrimSpace(directModel.cfg.Model)
	if strings.TrimSpace(expansion.Model) != "" {
		requestModel = strings.TrimSpace(expansion.Model)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	streamCh := make(chan streamEvent)
	go directModel.runRequestStream(ctx, cancel, resolvedPrompt, requestModel, expansion, streamCh)

	var rawResponse strings.Builder
	var preparedDocs []search.Document
	var cacheQuery string
	var cacheDocs []search.Document

	for event := range streamCh {
		if event.err != nil {
			return "", event.err
		}
		if event.preparedPrompt != "" {
			preparedDocs = append([]search.Document(nil), event.preparedDocs...)
			cacheQuery = strings.TrimSpace(event.cacheQuery)
			cacheDocs = append([]search.Document(nil), event.cacheDocs...)
		}
		if event.chunk != "" {
			rawResponse.WriteString(event.chunk)
		}
	}

	finalOutput := strings.TrimSpace(rawResponse.String())
	directModel.logSessionEntry("llm_full_response", finalOutput)
	if finalOutput == "" {
		return "", nil
	}

	if len(preparedDocs) > 0 {
		finalOutput = directModel.appendSyntheticSourcesIfMissing(finalOutput, preparedDocs)
	}
	if _, answer, hasReasoning := splitThinkingOutput(finalOutput); hasReasoning {
		finalOutput = answer
	}
	finalOutput = strings.TrimSpace(finalOutput)

	if finalOutput != "" && cacheQuery != "" && len(cacheDocs) > 0 {
		if done := directModel.searchBuilder.PersistSemanticCache(cacheQuery, cacheDocs, nil); done != nil {
			<-done
		}
	}

	return finalOutput, nil
}

func normalizeDirectMode(value string) (interactionMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(modeNormal):
		return modeNormal, nil
	case string(modeReasoning), "thinking":
		return modeReasoning, nil
	default:
		return "", fmt.Errorf("unsupported direct mode %q: use normal or reasoning", value)
	}
}

func validateDirectPrompt(prompt string) error {
	parts := strings.Fields(strings.TrimSpace(prompt))
	if len(parts) == 0 {
		return fmt.Errorf("missing prompt for direct mode")
	}
	if strings.EqualFold(parts[0], slashCommandHelp) {
		return fmt.Errorf("slash command %s is not supported in direct mode", slashCommandHelp)
	}
	return nil
}

func closeModelRuntime(m *model) error {
	if m == nil {
		return nil
	}
	if closer, ok := m.searchBuilder.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			return err
		}
	}
	if m.feedbackStore != nil {
		if err := m.feedbackStore.Close(); err != nil {
			return err
		}
	}
	if m.sessionLogger != nil {
		if err := m.sessionLogger.close(); err != nil {
			return err
		}
	}
	return nil
}
