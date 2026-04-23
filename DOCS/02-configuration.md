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
editor: neovim
theme: default
```

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
