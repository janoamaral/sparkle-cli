package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

func (m model) View() string {
	status := m.styles.status.Render(m.status)
	if m.spinnerVisible {
		status = m.spinner.View() + " " + status
	}

	help := m.styles.help.Render("󰌑 envia, 󰘳+O acepta, 󰘳+C cancela una solicitud o sale, 󱊷 sale.")
	body := lipgloss.JoinVertical(lipgloss.Left,
		m.viewport.View(),
		status,
		m.input.View(),
		help,
	)

	return m.styles.frame.Render(body)
}

func (m *model) refreshViewport() {
	parts := make([]string, 0, len(m.blocks))
	for _, block := range m.blocks {
		if strings.TrimSpace(block.rendered) == "" {
			continue
		}
		parts = append(parts, block.rendered)
	}
	m.viewport.SetContent(strings.Join(parts, "\n\n"))
	m.viewport.GotoBottom()
}

func (m *model) rebuildRenderer() {
	wrap := 100
	if m.viewport.Width > 0 {
		wrap = m.viewport.Width - 4
		if wrap < 20 {
			wrap = 20
		}
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(wrap),
	)
	if err != nil {
		return
	}
	m.renderer = renderer
	for index := range m.blocks {
		m.renderBlock(&m.blocks[index])
	}
}
