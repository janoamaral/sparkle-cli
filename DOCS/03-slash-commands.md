# Slash commands

[Back to docs index](README.md) | [Previous: Configuration](02-configuration.md) | [Next: Search and semantic cache](04-search-and-semantic-cache.md)

Slash commands are shortcuts expanded before sending content to Ollama.

## Built-in commands

- `/explain`
- `/fix`
- `/cheat`
- `/generate-code`
- `/search`
- `/translate`

## Examples

- `/explain ls -la`
- `/fix kubectl get pods -A --namspace kube-system`
- `/cheat find . -name '*.go'`
- `/generate-code list all files larger than 500MB`
- `/search how to change sudo prompt`
- `/translate english Esto es una prueba`

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
- `kind`: special behavior (`search`)

## Translate behavior

`/translate` accepts the first argument as the target language and returns only the translation text.
