# Configuration

[Back to docs index](README.md) | [Previous: Install and first run](01-install-and-first-run.md) | [Next: Slash commands](03-slash-commands.md)

## Config file location

By default, sparkle-cli loads config from:

- `~/.config/sparkle-cli/config.yaml`

You can override with:

```bash
sparkle-cli --config /path/to/config.yaml
```

## Minimal example

```yaml
ollama_url: http://localhost:11434
model: gemma4
search_url: https://search.nest.com.ar/search
search_embedding_model: nomic-embed-text
search_query_model: gemma3:270m
logs: false
editor: neovim
theme: default
```

`logs: true` enables per-session debug files named `session-[random].log` in the same directory as your active config file.

Session logs include timestamped entries for:

- `user_input`
- `model_used`
- `prompt_sent_to_model`
- `system_prompt_sent_to_model`
- `llm_full_response`

## Timeouts

Available timeout keys:

- `timeout` (legacy fallback)
- `search_timeout` (web search phase)
- `llm_resolve_timeout` (query rewrite / preparation)
- `llm_timeout` (final model response)

If specific timeouts are omitted, sparkle-cli falls back to defaults and/or legacy `timeout` behavior.

## Environment variable expansion

Config values support environment interpolation with `${VAR}`.

Example:

```yaml
qdrant_api_key: ${QDRANT_API_KEY}
```

## Editor selection

Supported values:

- `neovim` (default)
- `vim`
- `vscode` / `visual studio code`
- `emacs`

## Slash commands source

You can define commands inline in `commands` and/or in a separate file using:

```yaml
slash_commands_file: ./slash-commands.yaml
```

Both sources are merged. Inline `commands` win on collisions.

## Live editing with `/config`

You can edit and reload configuration from inside the TUI:

- Type `/config` and press `Enter`.
- sparkle-cli opens a temporary copy of the active config in your configured editor.
- Save and close the editor to trigger validation.
- If valid, sparkle-cli replaces the active config file and hot-reloads settings immediately.
- If invalid, sparkle-cli keeps the current config unchanged and reports the error.
