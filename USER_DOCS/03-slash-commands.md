# Slash commands

[Back to docs index](README.md) | [Previous: Configuration](02-configuration.md) | [Next: Search and semantic cache](04-search-and-semantic-cache.md)

Slash commands are shortcuts expanded before sending content to Ollama.

## Built-in commands

- `/explain`
- `/fix`
- `/cheat`
- `/generate-code`
- `/search`
- `/config`

## Examples

- `/explain ls -la`
- `/fix kubectl get pods -A --namspace kube-system`
- `/cheat find . -name '*.go'`
- `/generate-code list all files larger than 500MB`
- `/search how to change sudo prompt`
- `/config`

## Autocomplete

- Type `/` to start command suggestions.
- Press `Tab` to autocomplete.

## Custom commands (YAML)

Example (`slash-commands.yaml`):

```yaml
commands:
  - command: ticket
    prompt: |
      Generate a Jira ticket in {{.lang}} from this description:
      {{.Input}}
    params:
      required: [lang]
      optional: [role]
    model: gemma4
```

You can also set `slash_commands_dir` to a directory and keep one command per YAML file.

Example in your main config:

```yaml
slash_commands_file: ./slash-commands.yaml
slash_commands_dir: ./slash-commands
```

Example directory layout:

```text
slash-commands/
  10-ticket.yaml
  20-incident.yaml
```

Example `10-ticket.yaml`:

```yaml
command: ticket
prompt: |
  Generate a Jira ticket in {{.lang}} for role {{.role}} from this description:
  {{.Input}}
params:
  required: [lang]
  optional: [role]
model: gemma4
```

Usage:

```text
/ticket lang=en role=backend Add environment variables support and remove hardcoded values
```

## Supported command fields

- `command`: command name without `/`
- `prompt`: template using Go template variables (`{{.Input}}`, `{{.lang}}`, `{{.role}}`, `{{.pwd}}`)
- `template`: alias of `prompt` (same Go template syntax)
- `params`: named `name=value` args before free input
- `params.required`: required named args
- `params.optional`: optional named args
- `system`: per-command system prompt override (supports `{{.Input}}`, `{{.param_name}}`, `{{.pwd}}`)
- `model`: per-command model override
- `kind`: special behavior (`search`, `config`)

Template variables:

- `{{.Input}}`: remaining free-form input after named params.
- `{{.param_name}}`: named params declared in `params.required` or `params.optional`.
- `{{.pwd}}`: current working directory where sparkle-cli was invoked.

`params` remains backward compatible with the legacy list format:

```yaml
params: [lang]
```

## Config hot reload (`/config`)

- Run `/config` with no extra arguments.
- sparkle-cli opens a temporary copy of the currently active config file in your configured editor.
- When you save and close the editor, sparkle-cli validates and parses that temporary file.
- If validation succeeds, the temporary file replaces the active config file and the TUI hot-reloads config immediately.
- If validation fails, the active config file is not modified and an error is shown with the preserved temp file path.

Notes:

- `/config` does not call the LLM.
- On successful reload, `/config` is not added as a user message block in conversation history.
- On successful reload, input is cleared so you can continue with a new command immediately.

