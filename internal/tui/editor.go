package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/logico/sparkle-cli/internal/config"
	"github.com/logico/sparkle-cli/internal/i18n"
)

type editorDoneMsg struct {
	content     string
	editorLabel string
	err         error
}

func editInExternalEditor(localizer *i18n.Localizer, editor, content string) tea.Cmd {
	normalized, err := config.NormalizeEditor(editor)
	if err != nil {
		return func() tea.Msg {
			return editorDoneMsg{err: err}
		}
	}

	label, binary, args := resolveEditorCommand(normalized)
	file, err := os.CreateTemp("", "sparkle-cli-*.md")
	if err != nil {
		return func() tea.Msg {
			return editorDoneMsg{err: fmt.Errorf(localizer.Get("editor.prepare_temp_failed"), label, err)}
		}
	}

	path := file.Name()
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return func() tea.Msg {
			return editorDoneMsg{err: fmt.Errorf(localizer.Get("editor.write_temp_failed"), label, err)}
		}
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return func() tea.Msg {
			return editorDoneMsg{err: fmt.Errorf(localizer.Get("editor.close_temp_failed"), label, err)}
		}
	}

	commandArgs := append(args, path)
	cmd := exec.Command(binary, commandArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		defer os.Remove(path)
		if runErr != nil {
			return editorDoneMsg{err: fmt.Errorf(localizer.Get("editor.open_failed"), label, runErr)}
		}

		updated, err := os.ReadFile(path)
		if err != nil {
			return editorDoneMsg{err: fmt.Errorf(localizer.Get("editor.read_updated_failed"), label, err)}
		}

		return editorDoneMsg{
			content:     strings.TrimRight(string(updated), "\n"),
			editorLabel: label,
		}
	})
}

func resolveEditorCommand(editor string) (label, binary string, args []string) {
	switch editor {
	case "vim":
		return "Vim", "vim", nil
	case "vscode":
		return "Visual Studio Code", "code", []string{"--wait"}
	case "emacs":
		return "Emacs", "emacs", nil
	case "neovim":
		fallthrough
	default:
		return "Neovim", "nvim", nil
	}
}
