package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

type configReloadDoneMsg struct {
	cfg         config.Config
	path        string
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

func editConfigInExternalEditor(localizer *i18n.Localizer, editor string, configPath string) tea.Cmd {
	normalized, err := config.NormalizeEditor(editor)
	if err != nil {
		return func() tea.Msg {
			return configReloadDoneMsg{err: err}
		}
	}

	label, binary, args := resolveEditorCommand(normalized)
	resolvedPath, err := resolveConfigPath(configPath)
	if err != nil {
		return func() tea.Msg {
			return configReloadDoneMsg{err: err}
		}
	}

	tmpPath, err := prepareTempConfigFile(localizer, label, resolvedPath)
	if err != nil {
		return func() tea.Msg { return configReloadDoneMsg{err: err} }
	}

	commandArgs := append(args, tmpPath)
	cmd := exec.Command(binary, commandArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		if runErr != nil {
			_ = os.Remove(tmpPath)
			return configReloadDoneMsg{err: fmt.Errorf(localizer.Get("editor.open_failed"), label, runErr)}
		}

		reloadedCfg, _, loadErr := config.Load(tmpPath)
		if loadErr != nil {
			return configReloadDoneMsg{err: fmt.Errorf("invalid configuration: %w (edited file preserved at %s)", loadErr, tmpPath)}
		}

		if err := os.Rename(tmpPath, resolvedPath); err != nil {
			return configReloadDoneMsg{err: fmt.Errorf("replace config file %s: %w", resolvedPath, err)}
		}

		return configReloadDoneMsg{
			cfg:         reloadedCfg,
			path:        resolvedPath,
			editorLabel: label,
		}
	})
}

func resolveConfigPath(configPath string) (string, error) {
	resolvedPath := strings.TrimSpace(configPath)
	if resolvedPath != "" {
		return resolvedPath, nil
	}
	defaultPath, err := config.DefaultPath()
	if err != nil {
		return "", fmt.Errorf("resolve config path: %w", err)
	}
	return defaultPath, nil
}

func prepareTempConfigFile(localizer *i18n.Localizer, editorLabel string, configPath string) (string, error) {
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return "", fmt.Errorf("create config directory %s: %w", configDir, err)
	}

	original, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read config file %s: %w", configPath, err)
	}

	tmpFile, err := os.CreateTemp(configDir, ".sparkle-cli-config-*.yaml")
	if err != nil {
		return "", fmt.Errorf(localizer.Get("editor.prepare_temp_failed"), editorLabel, err)
	}

	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(original); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf(localizer.Get("editor.write_temp_failed"), editorLabel, err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf(localizer.Get("editor.close_temp_failed"), editorLabel, err)
	}

	return tmpPath, nil
}
