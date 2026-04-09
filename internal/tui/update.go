package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/logico/sparkle-cli/internal/ollama"
)

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
	m.input.Width = max(20, msg.Width-6)
	m.viewport.Width = max(20, msg.Width-4)
	m.viewport.Height = max(5, msg.Height-7)
	m.rebuildRenderer()
	m.refreshViewport()
}

func (m *model) handleKeyMsg(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.requesting && m.cancel != nil {
			m.cancel()
			m.status = "Peticion cancelada"
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
			m.status = "Escribe una consulta o un comando slash."
			return true, nil
		}
		return true, m.startRequest(prompt)
	case "ctrl+o":
		if m.requesting {
			return true, nil
		}
		candidate := strings.TrimSpace(m.lastAssistant())
		if candidate == "" {
			m.status = "No hay respuesta para aceptar todavia."
			return true, nil
		}
		m.acceptedOutput = candidate + "\n"
		m.exitCode = 0
		return true, tea.Quit
	default:
		return false, nil
	}
}

func (m *model) handleStreamChunk(msg streamChunkMsg) tea.Cmd {
	m.spinnerVisible = false
	m.lastTokenAt = time.Now()
	current := m.lastAssistant() + msg.content
	m.updateBlock(m.activeBlockIndex, current)
	m.status = "Recibiendo respuesta..."
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
	m.status = "Ctrl+O inserta la respuesta en BUFFER. Enter envia otra consulta."
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
	m.status = "Ocurrió un error. Puedes reintentar."
	m.finishRequest()
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

func (m *model) lastAssistant() string {
	if m.activeBlockIndex < 0 || m.activeBlockIndex >= len(m.blocks) {
		return ""
	}
	if m.blocks[m.activeBlockIndex].role != "assistant" {
		return ""
	}
	return m.blocks[m.activeBlockIndex].raw
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
