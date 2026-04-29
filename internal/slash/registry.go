package slash

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"
	"unicode"

	"github.com/logico/sparkle-cli/internal/config"
)

const errSlashRequiresInput = "slash command /%s requires input"

type Expansion struct {
	Prompt       string
	Used         bool
	Model        string
	Kind         string
	SystemPrompt string
}

const (
	KindTemplate = "template"
	KindSearch   = "search"
	KindConfig   = "config"
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

	kind := strings.TrimSpace(command.Kind)
	if kind == "" {
		kind = KindTemplate
	}

	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, parts[0]))
	if expansion, handled, err := resolveSpecialKind(commandName, command, kind, payload); handled {
		if err != nil {
			return Expansion{}, err
		}
		return expansion, nil
	}

	if payload == "" {
		return Expansion{}, fmt.Errorf(errSlashRequiresInput, commandName)
	}

	data, promptInput, err := templateDataForCommand(commandName, command, payload)
	if err != nil {
		return Expansion{}, err
	}
	if strings.TrimSpace(promptInput) == "" {
		return Expansion{}, fmt.Errorf(errSlashRequiresInput, commandName)
	}

	templateText := strings.TrimSpace(command.Template)
	if templateText == "" {
		templateText = strings.TrimSpace(command.Prompt)
	}
	if templateText == "" {
		return Expansion{}, fmt.Errorf("slash command /%s has empty prompt", commandName)
	}

	expandedPrompt, err := renderPrompt(commandName, templateText, data)
	if err != nil {
		return Expansion{}, err
	}

	expandedSystem, err := resolveSystemPrompt(commandName, strings.TrimSpace(command.System), data)
	if err != nil {
		return Expansion{}, err
	}

	model := strings.TrimSpace(command.Model)

	return Expansion{Prompt: expandedPrompt, Used: true, Model: model, Kind: kind, SystemPrompt: expandedSystem}, nil
}

func resolveSpecialKind(commandName string, command config.SlashCommand, kind string, payload string) (Expansion, bool, error) {
	if kind == KindConfig {
		if payload != "" {
			return Expansion{}, true, fmt.Errorf("slash command /%s does not accept input", commandName)
		}
		data := systemTemplateData("")
		expandedSystem, err := resolveSystemPrompt(commandName, strings.TrimSpace(command.System), data)
		if err != nil {
			return Expansion{}, true, err
		}
		return Expansion{Prompt: "", Used: true, Kind: kind, SystemPrompt: expandedSystem}, true, nil
	}

	if kind == KindSearch {
		if payload == "" {
			return Expansion{}, true, fmt.Errorf(errSlashRequiresInput, commandName)
		}
		data := systemTemplateData(payload)
		expandedSystem, err := resolveSystemPrompt(commandName, strings.TrimSpace(command.System), data)
		if err != nil {
			return Expansion{}, true, err
		}
		model := strings.TrimSpace(command.Model)
		return Expansion{Prompt: payload, Used: true, Model: model, Kind: kind, SystemPrompt: expandedSystem}, true, nil
	}

	return Expansion{}, false, nil
}

func Expand(input string, cfg config.Config) (string, bool, error) {
	expansion, err := Resolve(input, cfg)
	if err != nil {
		return "", false, err
	}
	return expansion.Prompt, expansion.Used, nil
}

func templateDataForCommand(commandName string, command config.SlashCommand, payload string) (map[string]string, string, error) {
	data := map[string]string{}
	assignTemplateValue(data, "Input", payload)
	assignTemplateValue(data, "Text", payload)
	assignTemplateValue(data, "pwd", currentWorkingDirectory())

	input := payload
	if len(command.Params) > 0 || len(command.Optional) > 0 {
		for _, name := range appendConfiguredParams(command.Params, command.Optional) {
			assignTemplateValue(data, name, "")
		}

		parsedParams, remainder, err := parseNamedParams(payload, command.Params, command.Optional)
		if err != nil {
			return nil, "", fmt.Errorf("slash command /%s %w", commandName, err)
		}
		input = remainder
		assignTemplateValue(data, "Input", remainder)
		assignTemplateValue(data, "Text", remainder)
		for name, value := range parsedParams {
			assignTemplateValue(data, name, value)
		}
	}

	return data, input, nil
}

func parseNamedParams(payload string, requiredParams []string, optionalParams []string) (map[string]string, string, error) {
	required := make(map[string]struct{}, len(requiredParams))
	allowed := make(map[string]struct{}, len(requiredParams)+len(optionalParams))
	for _, param := range requiredParams {
		name := strings.TrimSpace(param)
		required[name] = struct{}{}
		allowed[name] = struct{}{}
	}
	for _, param := range optionalParams {
		name := strings.TrimSpace(param)
		allowed[name] = struct{}{}
	}

	values := make(map[string]string, len(allowed))
	remaining := strings.TrimSpace(payload)
	for remaining != "" {
		field, rest := splitLeadingArgument(remaining)
		name, value, ok := strings.Cut(field, "=")
		if !ok {
			break
		}
		name = strings.TrimSpace(name)
		if _, exists := allowed[name]; !exists {
			break
		}
		if strings.TrimSpace(value) == "" {
			return nil, "", fmt.Errorf("requires param %s with a value", name)
		}
		values[name] = strings.TrimSpace(value)
		remaining = strings.TrimLeftFunc(rest, unicode.IsSpace)
	}

	missing := make([]string, 0)
	for _, param := range requiredParams {
		if _, ok := values[param]; !ok {
			missing = append(missing, param)
		}
	}
	if len(missing) > 0 {
		return nil, "", fmt.Errorf("requires params: %s", strings.Join(missing, ", "))
	}

	return values, remaining, nil
}

func appendConfiguredParams(requiredParams []string, optionalParams []string) []string {
	if len(requiredParams) == 0 && len(optionalParams) == 0 {
		return nil
	}
	out := make([]string, 0, len(requiredParams)+len(optionalParams))
	out = append(out, requiredParams...)
	out = append(out, optionalParams...)
	return out
}

func splitLeadingArgument(value string) (string, string) {
	value = strings.TrimLeftFunc(value, unicode.IsSpace)
	if value == "" {
		return "", ""
	}
	index := strings.IndexFunc(value, unicode.IsSpace)
	if index == -1 {
		return value, ""
	}
	return value[:index], value[index:]
}

func assignTemplateValue(data map[string]string, name string, value string) {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return
	}
	data[trimmedName] = value
	data[strings.ToLower(trimmedName)] = value
	data[toTemplateKey(trimmedName)] = value
}

func toTemplateKey(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_'
	})
	if len(parts) == 0 {
		return name
	}
	for index, part := range parts {
		if part == "" {
			continue
		}
		parts[index] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
	}
	return strings.Join(parts, "")
}

func renderPrompt(commandName string, templateText string, data map[string]string) (string, error) {
	tmpl, err := template.New(commandName).Option("missingkey=error").Parse(templateText)
	if err != nil {
		return "", fmt.Errorf("parse slash template /%s: %w", commandName, err)
	}

	var builder bytes.Buffer
	if err := tmpl.Execute(&builder, data); err != nil {
		return "", fmt.Errorf("execute slash template /%s: %w", commandName, err)
	}
	return builder.String(), nil
}

func resolveSystemPrompt(commandName string, systemTemplate string, data map[string]string) (string, error) {
	systemTemplate = strings.TrimSpace(systemTemplate)
	if systemTemplate == "" {
		return "", nil
	}
	rendered, err := renderPrompt(commandName+"#system", systemTemplate, data)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(rendered), nil
}

func systemTemplateData(input string) map[string]string {
	data := map[string]string{}
	assignTemplateValue(data, "Input", input)
	assignTemplateValue(data, "Text", input)
	assignTemplateValue(data, "pwd", currentWorkingDirectory())
	return data
}

func currentWorkingDirectory() string {
	pwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return pwd
}
