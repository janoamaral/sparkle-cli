package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/logico/sparkle-cli/internal/ollama"
	"github.com/logico/sparkle-cli/internal/search"
)

var writeClipboard = clipboard.WriteAll

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.handleWindowSize(msg)
	case tea.KeyMsg:
		quit, cmd := m.handleKeyMsg(msg)
		if quit {
			return m, cmd
		}
		if cmd != nil {
			return m, cmd
		}
	case streamProgressMsg:
		return m, m.handleStreamProgress(msg)
	case streamPreparedMsg:
		return m, m.handleStreamPrepared(msg)
	case streamChunkMsg:
		return m, m.handleStreamChunk(msg)
	case streamDoneMsg:
		return m, m.handleStreamDone()
	case streamErrMsg:
		m.handleStreamErr(msg)
	case cachePersistProgressMsg:
		return m, m.handleCachePersistProgress(msg)
	case cachePersistDoneMsg:
		m.cachePersistCh = nil
	case sourceLoadedMsg:
		m.sourceBusy = false
		m.sourceCancel = nil
		m.spinnerVisible = false
		m.state = stateSourceView
		m.sourceMode = sourceModeViewing
		m.sourceSelectionIndex = msg.index
		loaded := msg.document
		m.sourceDocument = &loaded
		m.input.SetValue("")
		m.input.Focus()
		m.input.CursorEnd()
		m.syncPaneLayout()
		m.refreshViewport()
		m.refreshSidebar()
		m.setStatus(m.localizer.Get("status.source_opened"))
	case sourceLoadErrMsg:
		m.sourceBusy = false
		m.sourceCancel = nil
		m.spinnerVisible = false
		m.state = stateSourceSelect
		m.sourceMode = sourceModeSelecting
		m.input.Focus()
		m.syncPaneLayout()
		m.refreshViewport()
		m.refreshSidebar()
		m.setStatus(m.formatRequestError(msg.err))
	case sourceAnswerMsg:
		m.sourceBusy = false
		m.sourceCancel = nil
		m.spinnerVisible = false
		m.sidebarTurns = append(m.sidebarTurns, sourceSidebarTurn{role: "assistant", content: msg.answer})
		m.input.SetValue("")
		m.input.Focus()
		m.input.CursorEnd()
		m.refreshSidebar()
		m.setStatus(m.localizer.Get("status.response_added"))
	case sourceAnswerErrMsg:
		m.sourceBusy = false
		m.sourceCancel = nil
		m.spinnerVisible = false
		m.input.Focus()
		m.refreshSidebar()
		m.setStatus(m.formatRequestError(msg.err))
	case editorDoneMsg:
		m.handleEditorDone(msg)
	case configReloadDoneMsg:
		m.handleConfigReloadDone(msg)
	case idleTickMsg:
		cmds = append(cmds, m.handleIdleTick()...)
	case spinner.TickMsg:
		cmds = append(cmds, m.handleSpinnerTick(msg)...)
	}

	cmds = append(cmds, m.updateComponents(msg)...)

	return m, tea.Batch(cmds...)
}

func (m *model) handleWindowSize(msg tea.WindowSizeMsg) {
	m.width = msg.Width
	m.height = msg.Height
	horizontalFrame := m.styles.frame.GetHorizontalFrameSize() + 1
	contentWidth := max(20, msg.Width-horizontalFrame)
	m.syncPaneLayout()
	m.input.Width = max(20, contentWidth-m.styles.inputBox.GetHorizontalFrameSize())
	m.refreshViewport()
	m.refreshSidebar()
}

func (m *model) syncPaneWidths() {
	mainWidth := m.mainPaneWidth()
	m.viewport.Width = mainWidth
	m.viewport.Style = lipgloss.NewStyle().Background(lipgloss.Color(m.colors.bgBase)).Width(mainWidth)
	sidebarWidth := m.sidebarContentWidth()
	m.sidebar.Width = sidebarWidth
	m.sidebar.Style = lipgloss.NewStyle().Background(lipgloss.Color(m.colors.bgRaised)).Width(sidebarWidth)
}

func (m *model) syncPaneLayout() {
	m.syncPaneWidths()
	m.rebuildRenderer()
	m.syncViewportLayout()
}

func (m *model) handleKeyMsg(msg tea.KeyMsg) (bool, tea.Cmd) {
	if handled, cmd := m.handleHelpModalKey(msg); handled {
		return true, cmd
	}

	if handled, cmd := m.handleExitKey(msg); handled {
		return true, cmd
	}
	if handled, cmd := m.handleSourceSearchModalKey(msg); handled {
		return true, cmd
	}
	if handled, cmd := m.handleSourceModeKey(msg); handled {
		return true, cmd
	}

	// Handle slash autocomplete keys
	if m.slashAutocompleteOpen {
		if handled, cmd := m.handleSlashAutocompleteKey(msg); handled {
			return true, cmd
		}
	}

	switch msg.String() {
	case "ctrl+s":
		return true, m.openSourceSelection()
	case "ctrl+p":
		m.openHelpModal()
		return true, nil
	case "enter":
		return true, m.handleEnterKey()
	case "ctrl+o":
		return true, m.acceptLatestAssistant()
	case "ctrl+l":
		return true, m.clearConversation()
	case "ctrl+t":
		return true, m.cycleInteractionMode()
	case "ctrl+k":
		return true, m.toggleReasoningView()
	case "ctrl+y":
		return true, m.copyLatestAssistant()
	case "ctrl+e":
		return true, m.editInput()
	default:
		if m.state == stateSourceSelect {
			if msg.String() >= "1" && msg.String() <= "9" {
				return true, m.openSourceByIndex(int(msg.String()[0] - '1'))
			}
		}
		return false, nil
	}
}

func (m *model) handleHelpModalKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !m.helpModalOpen {
		return false, nil
	}

	switch strings.ToLower(msg.String()) {
	case "esc":
		m.helpModalOpen = false
		m.helpModalScroll = 0
		return true, nil
	case "up":
		if m.helpModalScroll > 0 {
			m.helpModalScroll--
		}
		return true, nil
	case "down":
		if m.helpModalScroll < m.helpModalScrollLimit() {
			m.helpModalScroll++
		}
		return true, nil
	default:
		return true, nil
	}
}

func (m *model) handleSlashAutocompleteKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "up":
		m.slashAutocompleteIndex--
		if m.slashAutocompleteIndex < 0 {
			m.slashAutocompleteIndex = len(m.filteredSlashCommands) - 1
		}
		return true, nil
	case "down":
		m.slashAutocompleteIndex++
		if m.slashAutocompleteIndex >= len(m.filteredSlashCommands) {
			m.slashAutocompleteIndex = 0
		}
		return true, nil
	case "enter":
		if m.slashAutocompleteIndex >= 0 && m.slashAutocompleteIndex < len(m.filteredSlashCommands) {
			selectedCmd := m.filteredSlashCommands[m.slashAutocompleteIndex]
			// Replace the partial command with the full command
			m.input.SetValue("/" + selectedCmd + " ")
			m.input.CursorEnd()
			// Close autocomplete
			m.slashAutocompleteOpen = false
			m.filteredSlashCommands = nil
			m.slashAutocompleteIndex = -1
			m.slashAutocompletePrefix = ""
		}
		return true, nil
	case "esc":
		m.slashAutocompleteOpen = false
		m.filteredSlashCommands = nil
		m.slashAutocompleteIndex = -1
		m.slashAutocompletePrefix = ""
		return true, nil
	}
	return false, nil
}

func (m *model) handleSourceSearchModalKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if m.state != stateSourceView || !m.sourceSearchModalOpen {
		return false, nil
	}

	switch strings.ToLower(msg.String()) {
	case "enter":
		m.executeSourceSearch(m.sourceSearchInput.Value())
		m.closeSourceSearchModal()
		return true, nil
	case "esc", "ctrl+f":
		m.closeSourceSearchModal()
		return true, nil
	default:
		return false, nil
	}
}

func (m *model) handleExitKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.requesting && m.cancel != nil {
			m.userCanceled = true
			m.cancel()
			m.setStatus(m.localizer.Get("status.request_canceled"))
			return true, nil
		}
		if m.sourceBusy && m.sourceCancel != nil {
			m.sourceCancel()
			m.sourceCancel = nil
			m.sourceBusy = false
			m.spinnerVisible = false
			return true, m.closeSourceMode()
		}
		if m.inSourceMode() {
			return true, m.closeSourceMode()
		}
		m.exitCode = 1
		return true, tea.Quit
	case "esc":
		if m.requesting {
			return true, nil
		}
		if m.inSourceMode() {
			return true, m.closeSourceMode()
		}
		m.exitCode = 1
		return true, tea.Quit
	default:
		return false, nil
	}
}

func (m *model) handleSourceModeKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !m.inSourceMode() && msg.String() != "ctrl+s" {
		return false, nil
	}

	switch strings.ToLower(msg.String()) {
	case "ctrl+f":
		if m.state == stateSourceView && m.sourceDocument != nil {
			m.openSourceSearchModal()
			m.setStatus(m.localizer.Get("status.source_search_prompt"))
			return true, nil
		}
	case "ctrl+n":
		if m.state == stateSourceView && m.sourceDocument != nil {
			m.cycleSourceSearch(1)
			return true, nil
		}
	case "ctrl+shift+n":
		if m.state == stateSourceView && m.sourceDocument != nil {
			m.cycleSourceSearch(-1)
			return true, nil
		}
	case "up":
		if m.inSourceMode() {
			m.viewport.ScrollUp(1)
			return true, nil
		}
	case "down":
		if m.inSourceMode() {
			m.viewport.ScrollDown(1)
			return true, nil
		}
	case "shift+up":
		if m.state == stateSourceView && m.sourceDocument != nil {
			m.sidebar.ScrollUp(1)
			return true, nil
		}
	case "shift+down":
		if m.state == stateSourceView && m.sourceDocument != nil {
			m.sidebar.ScrollDown(1)
			return true, nil
		}
	default:
		if m.state == stateSourceSelect && msg.String() >= "1" && msg.String() <= "9" {
			return true, m.openSourceByIndex(int(msg.String()[0] - '1'))
		}
	}

	return false, nil
}

func (m *model) handleEnterKey() tea.Cmd {
	if m.requesting || m.sourceBusy {
		return nil
	}
	prompt := strings.TrimSpace(m.input.Value())
	if prompt == "" {
		m.setStatus(m.localizer.Get("status.write_message"))
		return nil
	}
	if m.state == stateSourceView && m.sourceDocument != nil {
		return m.startSourceQuestion(prompt)
	}
	return m.startRequest(prompt)
}

func (m *model) acceptLatestAssistant() tea.Cmd {
	if m.requesting {
		return nil
	}

	candidate := strings.TrimSpace(m.lastAssistant())
	if candidate == "" {
		m.setStatus(m.localizer.Get("status.no_response_accept"))
		return nil
	}

	m.acceptedOutput = candidate + "\n"
	m.exitCode = 0
	return tea.Quit
}

func (m *model) cycleInteractionMode() tea.Cmd {
	if m.requesting {
		return nil
	}
	m.cycleMode()
	m.setStatus(fmt.Sprintf(m.localizer.Get("status.mode_activated"), m.modeLabel()))
	return nil
}

func (m *model) copyLatestAssistant() tea.Cmd {
	if m.requesting {
		return nil
	}

	candidate := strings.TrimSpace(m.lastAssistant())
	if candidate == "" {
		m.setStatus(m.localizer.Get("status.no_response_copy"))
		return nil
	}
	if err := m.clipboardWrite(candidate); err != nil {
		m.setStatus(m.localizer.Get("status.copy_failed"))
		return nil
	}

	m.setStatus(m.localizer.Get("status.response_copied"))
	return nil
}

func (m *model) editInput() tea.Cmd {
	if m.requesting {
		return nil
	}

	if m.openInEditor == nil {
		m.setStatus(m.localizer.Get("status.editor_failed"))
		return nil
	}

	return m.openInEditor(m.localizer, m.cfg.Editor, m.input.Value())
}

func (m *model) handleStreamChunk(msg streamChunkMsg) tea.Cmd {
	m.spinnerVisible = false
	m.lastTokenAt = time.Now()
	m.setLLMTimerPhase(m.localizer.Get("status.receiving_llm_response"))
	m.markSearchResponseStarted()
	if m.activeBlockIndex < 0 {
		m.appendBlock("assistant", "")
		m.activeBlockIndex = len(m.blocks) - 1
	}
	current := m.lastAssistantRaw() + msg.content
	m.advanceReasoningPulse(current)
	m.updateBlock(m.activeBlockIndex, current)
	return waitForStream(m.streamCh)
}

func (m *model) handleStreamPrepared(msg streamPreparedMsg) tea.Cmd {
	m.pendingUserInput = msg.prompt
	m.pendingSearchDocs = append([]search.Document(nil), msg.docs...)
	m.pendingSearchCacheQuery = msg.cacheQuery
	m.pendingSearchCacheDocs = append([]search.Document(nil), msg.cacheDocs...)
	m.markSearchContextReady()
	m.updateProgress(search.ProgressUpdate{Key: progressKeyLLM, Kind: search.ProgressKindLLM, Text: m.localizer.Get("progress.llm_summarizing"), State: search.ProgressPending})
	m.startLLMTimer(m.localizer.Get("status.querying_ollama"))
	return waitForStream(m.streamCh)
}

func (m *model) handleCachePersistProgress(msg cachePersistProgressMsg) tea.Cmd {
	m.updateProgress(msg.update)
	switch msg.update.State {
	case search.ProgressDone:
		m.setStatus(m.localizer.Get("status.cache_updated"))
	case search.ProgressInfo:
		m.setStatus(m.cachePersistStatusText(msg.update))
	default:
		m.setStatus(m.localizer.Get("status.saving_cache"))
	}
	return waitForBackgroundMsg(m.cachePersistCh)
}

func (m *model) cachePersistStatusText(update search.ProgressUpdate) string {
	text := strings.TrimSpace(update.Text)
	if text == "" {
		return m.localizer.Get("status.cache_update_failed")
	}
	return text
}

func (m *model) handleStreamProgress(msg streamProgressMsg) tea.Cmd {
	m.updateProgress(msg.update)
	switch msg.update.Key {
	case search.CacheLookupKey():
		if msg.update.State == search.ProgressDone {
			m.setStatus(m.localizer.Get("status.reusing_cache"))
		} else if msg.update.State == search.ProgressInfo && strings.Contains(strings.ToLower(msg.update.Text), "continuando con busqueda web") {
			m.setStatus(m.localizer.Get("status.semantic_cache_continue"))
		} else {
			m.setStatus(m.localizer.Get("status.consulting_cache"))
		}
	case progressKeyRewrite:
		m.setStatus(m.localizer.Get("progress.rewrite_query") + "...")
	case progressKeySearch:
		m.setStatus(m.localizer.Get("status.searching_sources"))
	case progressKeyDownloads, progressKeyDownloadsBk:
		m.setStatus(m.localizer.Get("status.downloading_sources"))
	case progressKeyChunking:
		m.setStatus(m.localizer.Get("status.processing_sources"))
	case search.CachePersistKey():
		switch msg.update.State {
		case search.ProgressDone:
			m.setStatus(m.localizer.Get("status.cache_updated"))
		case search.ProgressInfo:
			m.setStatus(m.cachePersistStatusText(msg.update))
		default:
			m.setStatus(m.localizer.Get("status.saving_cache"))
		}
	case progressKeyTokenUsage, progressKeyTokenFinal, progressKeyReduction:
		m.setStatus(m.localizer.Get("progress.prepare_context"))
	case progressKeyLLM:
		m.setStatus(m.localizer.Get("status.generating_response"))
	default:
		if strings.HasPrefix(msg.update.Key, progressKeyDownloadURL) {
			m.setStatus(m.localizer.Get("status.downloading_sources"))
		} else if strings.HasPrefix(msg.update.Key, progressKeyLLMSource) {
			m.setStatus(m.localizer.Get("progress.prepare_context"))
		} else {
			m.setStatus(m.localizer.Get("status.updating_progress"))
		}
	}
	return waitForStream(m.streamCh)
}

func (m *model) handleStreamDone() tea.Cmd {
	rawLLM := strings.TrimSpace(m.lastAssistantRaw())
	m.logSessionEntry("llm_full_response", rawLLM)
	m.requesting = false
	m.state = stateComplete
	m.spinnerVisible = false
	m.stopLLMTimer()
	m.freezeProgressDiagnostics()
	m.input.SetValue("")
	m.input.Focus()
	m.input.CursorEnd()
	if raw := strings.TrimSpace(m.lastAssistantRaw()); raw != "" && len(m.pendingSearchDocs) > 0 {
		finalized := m.appendSyntheticSourcesIfMissing(raw, m.pendingSearchDocs)
		if finalized != raw {
			m.updateBlock(m.activeBlockIndex, finalized)
		}
	}
	assistant := strings.TrimSpace(m.lastAssistant())
	if m.progressBlockIndex >= 0 {
		m.updateProgress(search.ProgressUpdate{Key: progressKeyLLM, Kind: search.ProgressKindLLM, Text: m.localizer.Get("progress.llm_summary_received"), State: search.ProgressDone})
	}
	if assistant != "" && m.pendingUserInput != "" {
		m.session = append(m.session, ollama.ChatMessage{Role: "user", Content: m.pendingUserInput})
		m.session = append(m.session, structToAssistant(assistant))
	}
	cacheQuery := m.pendingSearchCacheQuery
	cacheDocs := append([]search.Document(nil), m.pendingSearchCacheDocs...)
	m.lastSearchDocs = append([]search.Document(nil), m.pendingSearchDocs...)
	m.pendingUserInput = ""
	m.pendingSearchDocs = nil
	m.pendingSearchCacheQuery = ""
	m.pendingSearchCacheDocs = nil
	m.setStatus(m.localizer.Get("status.post_request"))
	m.finishRequest()
	if assistant == "" || cacheQuery == "" || len(cacheDocs) == 0 {
		return nil
	}

	msgCh := make(chan tea.Msg, 8)
	done := m.searchBuilder.PersistSemanticCache(cacheQuery, cacheDocs, func(update search.ProgressUpdate) {
		msgCh <- cachePersistProgressMsg{update: update}
	})
	m.cachePersistCh = msgCh
	go func() {
		if done != nil {
			<-done
		}
		msgCh <- cachePersistDoneMsg{}
		close(msgCh)
	}()
	return waitForBackgroundMsg(msgCh)
}

func (m *model) handleStreamErr(msg streamErrMsg) {
	m.requesting = false
	m.state = stateReady
	m.spinnerVisible = false
	m.stopLLMTimer()
	m.freezeProgressDiagnostics()
	message := m.formatRequestError(msg.err)
	if m.userCanceled && errors.Is(msg.err, context.Canceled) {
		message = m.localizer.Get("status.request_canceled")
	}
	m.appendBlock("error", message)
	m.input.Focus()
	m.pendingUserInput = ""
	m.pendingSearchDocs = nil
	m.setStatus(m.localizer.Get("status.request_failed_retry"))
	m.finishRequest()
}

func (m *model) formatRequestError(err error) string {
	var stageErr requestStageError
	if errors.As(err, &stageErr) {
		if errors.Is(stageErr.err, context.DeadlineExceeded) {
			if stageErr.stage == requestStageSearch {
				return m.localizer.Get("error.timeout_web_search")
			}
			return m.localizer.Get("error.timeout_llm")
		}
		if errors.Is(stageErr.err, context.Canceled) {
			if stageErr.stage == requestStageSearch {
				return m.localizer.Get("error.timeout_web_search")
			}
			return m.localizer.Get("error.timeout_llm")
		}
		if stageErr.stage == requestStageSearch {
			return fmt.Sprintf(m.localizer.Get("error.web_search"), stageErr.err.Error())
		}
		return fmt.Sprintf(m.localizer.Get("error.llm"), stageErr.err.Error())
	}

	if errors.Is(err, context.Canceled) {
		return m.localizer.Get("status.request_canceled")
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return m.localizer.Get("error.timeout_response")
	}

	return err.Error()
}

func (m *model) handleEditorDone(msg editorDoneMsg) {
	if msg.err != nil {
		m.setStatus(msg.err.Error())
		return
	}
	m.input.SetValue(msg.content)
	m.input.Focus()
	m.input.CursorEnd()
	if msg.editorLabel == "" {
		m.setStatus(m.localizer.Get("status.editor_updated"))
		return
	}
	m.setStatus(fmt.Sprintf(m.localizer.Get("status.editor_updated_from"), msg.editorLabel))
}

func (m *model) handleConfigReloadDone(msg configReloadDoneMsg) {
	if msg.err != nil {
		m.appendBlock("error", msg.err.Error())
		m.setStatus(msg.err.Error())
		m.input.Focus()
		m.input.CursorEnd()
		return
	}

	m.applyRuntimeConfig(msg.cfg, msg.path)
	m.input.SetValue("")
	m.input.Focus()
	m.input.CursorEnd()
	if msg.editorLabel == "" {
		m.setStatus(m.localizer.Get("status.config_reloaded"))
		return
	}
	m.setStatus(fmt.Sprintf(m.localizer.Get("status.config_reloaded_from"), msg.editorLabel))
}

func (m *model) handleIdleTick() []tea.Cmd {
	if !m.requesting {
		return nil
	}
	m.spinnerVisible = time.Since(m.lastTokenAt) > idleThreshold
	m.refreshLLMTimerDisplay()
	return []tea.Cmd{idleTick()}
}

func (m *model) handleSpinnerTick(msg spinner.TickMsg) []tea.Cmd {
	if !m.spinnerVisible {
		return nil
	}
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return []tea.Cmd{cmd}
}

func (m *model) updateComponents(msg tea.Msg) []tea.Cmd {
	cmds := make([]tea.Cmd, 0, 3)
	if !m.requesting {
		if !m.sourceBusy {
			if m.helpModalOpen {
				// Keep input and cursor frozen while help modal is open.
			} else if m.state == stateSourceView && m.sourceSearchModalOpen {
				var cmd tea.Cmd
				m.sourceSearchInput, cmd = m.sourceSearchInput.Update(msg)
				cmds = append(cmds, cmd)
			} else {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				cmds = append(cmds, cmd)
				// Update slash command autocomplete after input changes
				m.updateSlashAutocomplete()
			}
		}
	}

	forwardToViewport := true
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		forwardToViewport = keyMsg.String() == "up" || keyMsg.String() == "down"
		if m.helpModalOpen || (m.state == stateSourceView && m.sourceSearchModalOpen) {
			forwardToViewport = false
		}
	}

	if forwardToViewport {
		var viewportCmd tea.Cmd
		m.viewport, viewportCmd = m.viewport.Update(msg)
		cmds = append(cmds, viewportCmd)
	}

	if m.spinnerVisible {
		cmds = append(cmds, m.spinner.Tick)
	}

	return cmds
}

func (m *model) finishRequest() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.userCanceled = false
	m.llmTimerActive = false
	m.llmTimerStartedAt = time.Time{}
	m.llmTimerPhase = ""
	m.reasoningPulseStep = -1
	m.pendingSearchDocs = nil
}

func (m *model) lastAssistantRaw() string {
	if m.activeBlockIndex < 0 || m.activeBlockIndex >= len(m.blocks) {
		return ""
	}
	if m.blocks[m.activeBlockIndex].role != "assistant" {
		return ""
	}
	return m.blocks[m.activeBlockIndex].raw
}

func (m *model) lastAssistant() string {
	raw := m.lastAssistantRaw()
	_, answer, active := splitThinkingOutput(raw)
	if active {
		return answer
	}
	return raw
}

func structToAssistant(content string) ollama.ChatMessage {
	return ollama.ChatMessage{Role: "assistant", Content: content}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
