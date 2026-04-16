package slash

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/logico/sparkle-cli/internal/config"
)

type TemplateData struct {
	Input    string
	Language string
	Text     string
}

type Expansion struct {
	Prompt string
	Used   bool
	Model  string
	Kind   string
}

const (
	KindTemplate = "template"
	KindSearch   = "search"
)

func Resolve(input string, cfg config.Config) (Expansion, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") {
		return Expansion{Prompt: input, Used: false, Kind: KindTemplate}, nil
	}

	parts := strings.Fields(trimmed)
	commandName := strings.TrimPrefix(parts[0], "/")
	command, ok := cfg.Commands[commandName]
	if !ok {
		return Expansion{}, fmt.Errorf("unknown slash command: /%s", commandName)
	}

	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, parts[0]))
	if payload == "" {
		return Expansion{}, fmt.Errorf("slash command /%s requires input", commandName)
	}

	kind := strings.TrimSpace(command.Kind)
	if kind == "" {
		kind = KindTemplate
	}
	if kind == KindSearch {
		model := strings.TrimSpace(command.Model)
		return Expansion{Prompt: payload, Used: true, Model: model, Kind: kind}, nil
	}

	data := TemplateData{Input: payload, Text: payload}
	if len(parts) > 1 {
		data.Language = parts[1]
		data.Text = strings.TrimSpace(strings.TrimPrefix(payload, parts[1]))
	}
	if commandName == "translate" && (strings.TrimSpace(data.Language) == "" || strings.TrimSpace(data.Text) == "") {
		return Expansion{}, fmt.Errorf("slash command /translate requires a target language and text")
	}

	tmpl, err := template.New(commandName).Option("missingkey=error").Parse(command.Template)
	if err != nil {
		return Expansion{}, fmt.Errorf("parse slash template /%s: %w", commandName, err)
	}

	var builder bytes.Buffer
	if err := tmpl.Execute(&builder, data); err != nil {
		return Expansion{}, fmt.Errorf("execute slash template /%s: %w", commandName, err)
	}

	model := strings.TrimSpace(command.Model)
	if commandName == "translate" && model == "" {
		model = "translategemma"
	}

	return Expansion{Prompt: builder.String(), Used: true, Model: model, Kind: kind}, nil
}

func Expand(input string, cfg config.Config) (string, bool, error) {
	expansion, err := Resolve(input, cfg)
	if err != nil {
		return "", false, err
	}
	return expansion.Prompt, expansion.Used, nil
}
