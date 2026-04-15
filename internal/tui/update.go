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
	case streamChunkMsg:
		return m, m.handleStreamChunk(msg)
	case streamDoneMsg:
		m.handleStreamDone()
	case streamErrMsg:
		m.handleStreamErr(msg)
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
	m.viewport.Width = contentWidth
	m.viewport.Style = lipgloss.NewStyle().Background(lipgloss.Color(m.colors.bgBase)).Width(contentWidth)
	m.input.Width = max(20, contentWidth-m.styles.inputBox.GetHorizontalFrameSize())
	m.rebuildRenderer()
	m.syncViewportLayout()
	m.refreshViewport()
}

func (m *model) handleKeyMsg(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.requesting && m.cancel != nil {
			m.cancel()
			m.setStatus("Peticion cancelada")
			return true, nil
		}
		m.exitCode = 1
		return true, tea.Quit
	case "esc":
		if m.requesting {
			return true, nil
		}
		m.exitCode = 1
		return true, tea.Quit
	case "enter":
		if m.requesting {
			return true, nil
		}
		prompt := strings.TrimSpace(m.input.Value())
		if prompt == "" {
			m.setStatus("Escribe un mensaje o un slash command.")
			return true, nil
		}
		return true, m.startRequest(prompt)
	case "ctrl+o":
		return true, m.acceptLatestAssistant()
	case "ctrl+t":
		return true, m.toggleThinkingMode()
	case "ctrl+y":
		return true, m.copyLatestAssistant()
	case "ctrl+e":
		return true, m.editInput()
	default:
		return false, nil
	}
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

func (m *model) toggleThinkingMode() tea.Cmd {
	if m.requesting {
		return nil
	}
	m.thinkingEnabled = !m.thinkingEnabled
	m.setStatus("Modo " + m.thinkingModeLabel() + " activado.")
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
	if m.activeBlockIndex < 0 {
		m.appendBlock("assistant", "")
		m.activeBlockIndex = len(m.blocks) - 1
	}
	current := m.lastAssistantRaw() + msg.content
	m.updateBlock(m.activeBlockIndex, current)
	m.setStatus("Recibiendo respuesta...")
	return waitForStream(m.streamCh)
}

func (m *model) handleStreamDone() {
	m.requesting = false
	m.state = stateComplete
	m.spinnerVisible = false
	m.input.SetValue("")
	m.input.Focus()
	m.input.CursorEnd()
	assistant := strings.TrimSpace(m.lastAssistant())
	if assistant != "" {
		m.session = append(m.session, structToAssistant(assistant))
	}
	m.setStatus("Ctrl+E abre editor del input · Ctrl+O inserta en buffer · Ctrl+Y copia al clipboard · Enter envia otra consulta.")
	m.finishRequest()
}

func (m *model) handleStreamErr(msg streamErrMsg) {
	m.requesting = false
	m.state = stateReady
	m.spinnerVisible = false
	message := msg.err.Error()
	if errors.Is(msg.err, context.Canceled) {
		message = "Peticion cancelada"
	}
	if errors.Is(msg.err, context.DeadlineExceeded) {
		message = "Timeout esperando respuesta de Ollama"
	}
	m.appendBlock("error", message)
	m.input.Focus()
	m.setStatus("Ocurrió un error. Puedes reintentar.")
	m.finishRequest()
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
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
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
