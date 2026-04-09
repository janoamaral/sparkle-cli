package slash

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/logico/sparkle-cli/internal/config"
)

type TemplateData struct {
	Input string
}

func Expand(input string, cfg config.Config) (string, bool, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") {
		return input, false, nil
	}

	parts := strings.Fields(trimmed)
	commandName := strings.TrimPrefix(parts[0], "/")
	command, ok := cfg.Commands[commandName]
	if !ok {
		return "", false, fmt.Errorf("unknown slash command: /%s", commandName)
	}

	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, parts[0]))
	if payload == "" {
		return "", false, fmt.Errorf("slash command /%s requires input", commandName)
	}

	tmpl, err := template.New(commandName).Option("missingkey=error").Parse(command.Template)
	if err != nil {
		return "", false, fmt.Errorf("parse slash template /%s: %w", commandName, err)
	}

	var builder bytes.Buffer
	if err := tmpl.Execute(&builder, TemplateData{Input: payload}); err != nil {
		return "", false, fmt.Errorf("execute slash template /%s: %w", commandName, err)
	}

	return builder.String(), true, nil
}
