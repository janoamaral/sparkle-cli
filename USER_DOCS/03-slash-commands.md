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
      Generate a Jira ticket in {lang} from this description:
      {input}
    params: [lang]
    model: gemma4
```

Usage:

```text
/ticket lang=en Add environment variables support and remove hardcoded values
```

## Supported command fields

- `command`: command name without `/`
- `prompt`: preferred template with named placeholders (`{input}`, `{lang}`)
- `template`: legacy Go template style (`{{.Input}}`)
- `params`: required `name=value` args before free input
- `system`: per-command system prompt override
- `model`: per-command model override
- `kind`: special behavior (`search`, `config`)

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

