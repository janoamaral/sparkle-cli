package slash

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"text/template"
	"unicode"

	"github.com/logico/sparkle-cli/internal/config"
)

var placeholderPattern = regexp.MustCompile(`\{([a-zA-Z][a-zA-Z0-9_-]*)\}`)

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

	model := strings.TrimSpace(command.Model)
	if commandName == "translate" && model == "" {
		model = "translategemma"
	}

	return Expansion{Prompt: expandedPrompt, Used: true, Model: model, Kind: kind, SystemPrompt: strings.TrimSpace(command.System)}, nil
}

func resolveSpecialKind(commandName string, command config.SlashCommand, kind string, payload string) (Expansion, bool, error) {
	if kind == KindConfig {
		if payload != "" {
			return Expansion{}, true, fmt.Errorf("slash command /%s does not accept input", commandName)
		}
		return Expansion{Prompt: "", Used: true, Kind: kind, SystemPrompt: strings.TrimSpace(command.System)}, true, nil
	}

	if kind == KindSearch {
		if payload == "" {
			return Expansion{}, true, fmt.Errorf(errSlashRequiresInput, commandName)
		}
		model := strings.TrimSpace(command.Model)
		return Expansion{Prompt: payload, Used: true, Model: model, Kind: kind, SystemPrompt: strings.TrimSpace(command.System)}, true, nil
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

	input := payload
	if len(command.Params) > 0 {
		parsedParams, remainder, err := parseNamedParams(payload, command.Params)
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

	if commandName == "translate" && len(command.Params) == 0 {
		language, text := splitLeadingArgument(payload)
		text = strings.TrimSpace(text)
		if strings.TrimSpace(language) == "" || strings.TrimSpace(text) == "" {
			return nil, "", fmt.Errorf("slash command /translate requires a target language and text")
		}
		input = text
		assignTemplateValue(data, "Input", text)
		assignTemplateValue(data, "Text", text)
		assignTemplateValue(data, "Language", language)
		assignTemplateValue(data, "lang", language)
	}

	return data, input, nil
}

func parseNamedParams(payload string, params []string) (map[string]string, string, error) {
	required := make(map[string]struct{}, len(params))
	for _, param := range params {
		required[strings.TrimSpace(param)] = struct{}{}
	}

	values := make(map[string]string, len(params))
	remaining := strings.TrimSpace(payload)
	for remaining != "" {
		field, rest := splitLeadingArgument(remaining)
		name, value, ok := strings.Cut(field, "=")
		if !ok {
			break
		}
		name = strings.TrimSpace(name)
		if _, exists := required[name]; !exists {
			break
		}
		if strings.TrimSpace(value) == "" {
			return nil, "", fmt.Errorf("requires param %s with a value", name)
		}
		values[name] = strings.TrimSpace(value)
		remaining = strings.TrimLeftFunc(rest, unicode.IsSpace)
	}

	missing := make([]string, 0)
	for _, param := range params {
		if _, ok := values[param]; !ok {
			missing = append(missing, param)
		}
	}
	if len(missing) > 0 {
		return nil, "", fmt.Errorf("requires params: %s", strings.Join(missing, ", "))
	}

	return values, remaining, nil
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
	if placeholderPattern.MatchString(templateText) {
		return renderNamedPlaceholderPrompt(commandName, templateText, data)
	}

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

func renderNamedPlaceholderPrompt(commandName string, templateText string, data map[string]string) (string, error) {
	var renderErr error
	rendered := placeholderPattern.ReplaceAllStringFunc(templateText, func(match string) string {
		if renderErr != nil {
			return match
		}
		groups := placeholderPattern.FindStringSubmatch(match)
		if len(groups) != 2 {
			return match
		}
		key := groups[1]
		if value, ok := data[key]; ok {
			return value
		}
		if value, ok := data[strings.ToLower(key)]; ok {
			return value
		}
		renderErr = fmt.Errorf("slash command /%s missing value for {%s}", commandName, key)
		return match
	})
	if renderErr != nil {
		return "", renderErr
	}
	return rendered, nil
}
