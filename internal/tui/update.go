package tui

import (
	"context"
	"errors"
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

const canceledMessage = "Peticion cancelada"

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
		m.refreshViewport()
		m.refreshSidebar()
		m.setStatus("Fuente abierta. Usa flechas arriba/abajo para navegar y escribe preguntas en el sidebar.")
	case sourceLoadErrMsg:
		m.sourceBusy = false
		m.sourceCancel = nil
		m.spinnerVisible = false
		m.state = stateSourceSelect
		m.sourceMode = sourceModeSelecting
		m.input.Focus()
		m.refreshViewport()
		m.refreshSidebar()
		m.setStatus(formatRequestError(msg.err))
	case sourceAnswerMsg:
		m.sourceBusy = false
		m.sourceCancel = nil
		m.spinnerVisible = false
		m.sidebarTurns = append(m.sidebarTurns, sourceSidebarTurn{role: "assistant", content: msg.answer})
		m.input.SetValue("")
		m.input.Focus()
		m.input.CursorEnd()
		m.refreshSidebar()
		m.setStatus("Respuesta agregada al sidebar.")
	case sourceAnswerErrMsg:
		m.sourceBusy = false
		m.sourceCancel = nil
		m.spinnerVisible = false
		m.input.Focus()
		m.refreshSidebar()
		m.setStatus(formatRequestError(msg.err))
	case editorDoneMsg:
		m.handleEditorDone(msg)
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
	mainWidth := m.mainPaneWidth()
	m.viewport.Width = mainWidth
	m.viewport.Style = lipgloss.NewStyle().Background(lipgloss.Color(m.colors.bgBase)).Width(mainWidth)
	m.sidebar.Width = m.sidebarContentWidth()
	m.sidebar.Style = lipgloss.NewStyle().Background(lipgloss.Color(m.colors.bgRaised)).Width(m.sidebarContentWidth())
	m.input.Width = max(20, contentWidth-m.styles.inputBox.GetHorizontalFrameSize())
	m.rebuildRenderer()
	m.syncViewportLayout()
	m.refreshViewport()
	m.refreshSidebar()
}

func (m *model) handleKeyMsg(msg tea.KeyMsg) (bool, tea.Cmd) {
	if handled, cmd := m.handleExitKey(msg); handled {
		return true, cmd
	}
	if handled, cmd := m.handleSourceModeKey(msg); handled {
		return true, cmd
	}

	switch msg.String() {
	case "ctrl+s":
		return true, m.openSourceSelection()
	case "enter":
		return true, m.handleEnterKey()
	case "ctrl+o":
		return true, m.acceptLatestAssistant()
	case "ctrl+l":
		return true, m.clearConversation()
	case "ctrl+t":
		return true, m.cycleInteractionMode()
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

func (m *model) handleExitKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.requesting && m.cancel != nil {
			m.userCanceled = true
			m.cancel()
			m.setStatus(canceledMessage)
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

	switch msg.String() {
	case "up":
		if m.inSourceMode() {
			m.viewport.LineUp(1)
			return true, nil
		}
	case "down":
		if m.inSourceMode() {
			m.viewport.LineDown(1)
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
		m.setStatus("Escribe un mensaje o un slash command.")
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
		m.setStatus("No hay respuesta para aceptar todavia.")
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
	m.setStatus("Modo " + m.modeLabel() + " activado.")
	return nil
}

func (m *model) copyLatestAssistant() tea.Cmd {
	if m.requesting {
		return nil
	}

	candidate := strings.TrimSpace(m.lastAssistant())
	if candidate == "" {
		m.setStatus("No hay respuesta para copiar todavia.")
		return nil
	}
	if err := m.clipboardWrite(candidate); err != nil {
		m.setStatus("No se pudo copiar la respuesta al clipboard.")
		return nil
	}

	m.setStatus("Respuesta copiada al clipboard.")
	return nil
}

func (m *model) editInput() tea.Cmd {
	if m.requesting {
		return nil
	}

	if m.openInEditor == nil {
		m.setStatus("No se pudo inicializar el editor externo.")
		return nil
	}

	return m.openInEditor(m.cfg.Editor, m.input.Value())
}

func (m *model) handleStreamChunk(msg streamChunkMsg) tea.Cmd {
	m.spinnerVisible = false
	m.lastTokenAt = time.Now()
	m.setLLMTimerPhase("Recibiendo respuesta del LLM")
	m.markSearchResponseStarted()
	if m.activeBlockIndex < 0 {
		m.appendBlock("assistant", "")
		m.activeBlockIndex = len(m.blocks) - 1
	}
	current := m.lastAssistantRaw() + msg.content
	m.updateBlock(m.activeBlockIndex, current)
	return waitForStream(m.streamCh)
}

func (m *model) handleStreamPrepared(msg streamPreparedMsg) tea.Cmd {
	m.pendingUserInput = msg.prompt
	m.pendingSearchDocs = append([]search.Document(nil), msg.docs...)
	m.pendingSearchCacheQuery = msg.cacheQuery
	m.pendingSearchCacheDocs = append([]search.Document(nil), msg.cacheDocs...)
	m.markSearchContextReady()
	m.updateProgress(search.ProgressUpdate{Key: progressKeyLLM, Kind: search.ProgressKindLLM, Text: "Consultando LLM para resumir la información", State: search.ProgressPending})
	m.startLLMTimer("Consultando Ollama")
	return waitForStream(m.streamCh)
}

func (m *model) handleCachePersistProgress(msg cachePersistProgressMsg) tea.Cmd {
	m.updateProgress(msg.update)
	switch msg.update.State {
	case search.ProgressDone:
		m.setStatus("Cache semantica actualizada en Qdrant.")
	case search.ProgressInfo:
		m.setStatus(cachePersistStatusText(msg.update))
	default:
		m.setStatus("Guardando resultados en cache semantica...")
	}
	return waitForBackgroundMsg(m.cachePersistCh)
}

func cachePersistStatusText(update search.ProgressUpdate) string {
	text := strings.TrimSpace(update.Text)
	if text == "" {
		return "No se pudo actualizar la cache semantica."
	}
	return text
}

func (m *model) handleStreamProgress(msg streamProgressMsg) tea.Cmd {
	m.updateProgress(msg.update)
	switch msg.update.Key {
	case search.CacheLookupKey():
		if msg.update.State == search.ProgressDone {
			m.setStatus("Reutilizando cache semantica...")
		} else if msg.update.State == search.ProgressInfo && strings.Contains(strings.ToLower(msg.update.Text), "continuando con busqueda web") {
			m.setStatus("Cache semantica sin hits; buscando en la web...")
		} else {
			m.setStatus("Consultando cache semantica...")
		}
	case progressKeyRewrite:
		m.setStatus("Optimizando query...")
	case progressKeySearch:
		m.setStatus("Buscando fuentes en la web...")
	case progressKeyDownloads, progressKeyDownloadsBk:
		m.setStatus("Descargando fuentes...")
	case progressKeyChunking:
		m.setStatus("Procesando fuentes...")
	case search.CachePersistKey():
		if msg.update.State == search.ProgressDone {
			m.setStatus("Cache semantica actualizada en Qdrant.")
		} else if msg.update.State == search.ProgressInfo {
			m.setStatus(cachePersistStatusText(msg.update))
		} else {
			m.setStatus("Guardando resultados en cache semantica...")
		}
	case progressKeyTokenUsage, progressKeyTokenFinal, progressKeyReduction:
		m.setStatus("Preparando contexto...")
	case progressKeyLLM:
		m.setStatus("Generando respuesta...")
	default:
		if strings.HasPrefix(msg.update.Key, progressKeyDownloadURL) {
			m.setStatus("Descargando fuentes...")
		} else if strings.HasPrefix(msg.update.Key, progressKeyLLMSource) {
			m.setStatus("Preparando contexto...")
		} else {
			m.setStatus("Actualizando progreso...")
		}
	}
	return waitForStream(m.streamCh)
}

func (m *model) handleStreamDone() tea.Cmd {
	m.requesting = false
	m.state = stateComplete
	m.spinnerVisible = false
	m.stopLLMTimer()
	m.freezeProgressDiagnostics()
	m.input.SetValue("")
	m.input.Focus()
	m.input.CursorEnd()
	if raw := strings.TrimSpace(m.lastAssistantRaw()); raw != "" && len(m.pendingSearchDocs) > 0 {
		finalized := appendSyntheticSourcesIfMissing(raw, m.pendingSearchDocs)
		if finalized != raw {
			m.updateBlock(m.activeBlockIndex, finalized)
		}
	}
	assistant := strings.TrimSpace(m.lastAssistant())
	if m.progressBlockIndex >= 0 {
		m.updateProgress(search.ProgressUpdate{Key: progressKeyLLM, Kind: search.ProgressKindLLM, Text: "Resumen del LLM recibido", State: search.ProgressDone})
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
	m.setStatus(postRequestStatus)
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
	message := formatRequestError(msg.err)
	if m.userCanceled && errors.Is(msg.err, context.Canceled) {
		message = canceledMessage
	}
	m.appendBlock("error", message)
	m.input.Focus()
	m.pendingUserInput = ""
	m.pendingSearchDocs = nil
	m.setStatus("Ocurrió un error. Puedes reintentar.")
	m.finishRequest()
}

func formatRequestError(err error) string {
	var stageErr requestStageError
	if errors.As(err, &stageErr) {
		if errors.Is(stageErr.err, context.DeadlineExceeded) {
			if stageErr.stage == requestStageSearch {
				return "Timeout durante la busqueda web"
			}
			return "Timeout esperando respuesta del LLM"
		}
		if errors.Is(stageErr.err, context.Canceled) {
			if stageErr.stage == requestStageSearch {
				return "Timeout durante la busqueda web"
			}
			return "Timeout esperando respuesta del LLM"
		}
		if stageErr.stage == requestStageSearch {
			return "Error durante la busqueda web: " + stageErr.err.Error()
		}
		return "Error del LLM: " + stageErr.err.Error()
	}

	if errors.Is(err, context.Canceled) {
		return canceledMessage
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return "Timeout esperando respuesta"
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
		m.setStatus("Input actualizado desde el editor.")
		return
	}
	m.setStatus("Input actualizado desde " + msg.editorLabel + ".")
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
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	var viewportCmd tea.Cmd
	m.viewport, viewportCmd = m.viewport.Update(msg)
	cmds = append(cmds, viewportCmd)

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
