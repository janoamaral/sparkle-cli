# sparkle-cli

sparkle-cli is a Bubble Tea terminal assistant for shell work. It talks to Ollama over native HTTP, keeps an in-memory session, supports slash commands, and can hand a generated command back to Zsh.

## About This Project

> [!IMPORTANT]
> This is a personal project made 100% with AI assistance.
> The goal is to have fun while building and, at the same time, create a tool that is useful for my daily workflow.

## Demo

https://github.com/user-attachments/assets/53324380-0ca7-4dfd-90d5-4f72a49cadc1

## Quick Start

### Requirements

- Go 1.22+
- Ollama running locally or reachable through `ollama_url`
- Zsh with ZLE enabled

### Run

```bash
go run ./cmd/sparkle-cli --context "git log --oneline"
```

## End-User Documentation

Detailed feature documentation for end users is available in [USER_DOCS/](USER_DOCS/):

- [USER_DOCS/README.md](USER_DOCS/README.md) (feature index)
- [USER_DOCS/01-install-and-first-run.md](USER_DOCS/01-install-and-first-run.md)
- [USER_DOCS/02-configuration.md](USER_DOCS/02-configuration.md)
- [USER_DOCS/03-slash-commands.md](USER_DOCS/03-slash-commands.md)
- [USER_DOCS/04-search-and-semantic-cache.md](USER_DOCS/04-search-and-semantic-cache.md)
- [USER_DOCS/05-source-mode-and-sidebar.md](USER_DOCS/05-source-mode-and-sidebar.md)
- [USER_DOCS/06-interaction-modes.md](USER_DOCS/06-interaction-modes.md)
- [USER_DOCS/07-editor-clipboard-and-output.md](USER_DOCS/07-editor-clipboard-and-output.md)
- [USER_DOCS/08-zsh-integration.md](USER_DOCS/08-zsh-integration.md)
- [USER_DOCS/09-language-and-localization.md](USER_DOCS/09-language-and-localization.md)
- [USER_DOCS/10-search-workflow.md](USER_DOCS/10-search-workflow.md)

## Configuration

The config path follows XDG and defaults to `~/.config/sparkle-cli/config.yaml`.

A ready-to-copy example config is available at [examples/config/config.example.yaml](examples/config/config.example.yaml).

The example below shows all core fields:

```yaml
ollama_url: http://localhost:11434
search_url: https://search.nest.com.ar/search
search_embedding_model: nomic-embed-text
search_query_model: gemma3:270m
model: gemma4
system_prompt: |
  You are a terminal expert. Produce concise, correct shell guidance and prefer returning a single command when the user is asking for one.
timeout: 30
search_timeout: 60
llm_resolve_timeout: 90
llm_timeout: 240
qdrant_enabled: false
qdrant_host: qdrant.nest.com.ar
qdrant_port: 6334
qdrant_api_key: ""
qdrant_use_tls: true
qdrant_collection: semantic_cache
qdrant_score_threshold: 0.92
qdrant_ttl_hours: 48
qdrant_pool_size: 3
logs: false
editor: neovim
slash_commands_file: ./slash-commands.yaml
commands:
  explain:
    template: "Explain this command concisely: {{.Input}}"
  fix:
    template: "Fix the errors in this command: {{.Input}}"
  cheat:
    template: "Show usage examples for: {{.Input}}"
  generate-code:
    template: "Generate the shell command that matches this description. Return only the command, with no explanation or markdown: {{.Input}}"
  search:
    kind: search
  config:
    kind: config
```

Qdrant cache-first example:

```yaml
search_url: https://search.nest.com.ar/search
search_embedding_model: nomic-embed-text
search_query_model: gemma3:270m
qdrant_enabled: true
qdrant_host: qdrant.nest.com.ar
qdrant_port: 6334
qdrant_api_key: ${QDRANT_API_KEY}
qdrant_use_tls: true
qdrant_collection: semantic_cache
qdrant_score_threshold: 0.90
qdrant_ttl_hours: 48
qdrant_pool_size: 3
```

With `qdrant_enabled: true`, `/search` tries semantic cache first over Qdrant gRPC and only falls back to SearXNG when there is no fresh high-score match. Cached web evidence is chunked, deduplicated by SHA-256, and ingested in the background after a successful web fetch.

## TUI Usage

Key bindings inside the TUI:

- `Enter`: send the current prompt to Ollama
- `Tab`: autocomplete the active slash command or suggestion
- `Ctrl+T`: cycle between `Normal`, `Reasoning`, and `Chat` mode
- `Ctrl+E`: open the current input in your configured editor
- `Ctrl+L`: clear the current conversation
- `Ctrl+O`: accept the latest assistant response and print it to stdout
- `Ctrl+Y`: copy the latest assistant response to the clipboard
- `Ctrl+C`: cancel an in-flight request, or exit if idle
- `Esc`: exit without emitting a command

`Chat` mode sends the previous user and assistant messages as conversation context on each request. `Reasoning` mode keeps the existing thinking prompt behavior without adding prior turns.

Supported editors for `editor` are `neovim` (default), `vim`, `vscode`/`visual studio code`, and `emacs`.

## Zsh Bridge

Source `scripts/init.zsh` from your `.zshrc` after the binary is on `PATH`.

```zsh
source /path/to/sparkle-cli/scripts/init.zsh
```

The widget binds `Ctrl+G`. It captures `$BUFFER`, opens the TUI with `--context`, and only replaces `$BUFFER` when the process exits successfully and emits a non-empty command.

## Slash commands

Slash commands are expanded before the prompt is sent to Ollama.

You can keep them inline under `commands`, move them into a dedicated YAML file with `slash_commands_file`, and/or load a directory of YAML files with `slash_commands_dir`. Inline commands and imported commands are merged, and inline config wins if the same command is declared in multiple places.

- `/explain ls -la`
- `/fix kubectl get pods -A --namspace kube-system`
- `/cheat find . -name '*.go'`
- `/generate-code list the processes using port 3000`
- `/search how to change the sudo prompt message`
- `/config`

Dedicated slash commands file example:

```yaml
commands:
  - command: ticket
    prompt: |
      Create a Jira ticket in {{.lang}} based on the following description:
      {{.Input}}
    system: You are an expert software engineer that writes concise implementation tickets.
    params:
      required: [lang]
      optional: [role]
    model: gemma4
```

Directory-based layout (one command per file):

```text
slash-commands/
  10-ticket.yaml
  20-incident.yaml
```

Main config example:

```yaml
slash_commands_file: ./slash-commands.yaml
slash_commands_dir: ./slash-commands
```

Example `10-ticket.yaml`:

```yaml
command: ticket
prompt: |
  Create a Jira ticket in {{.lang}} with role {{.role}} based on:
  {{.Input}}
params:
  required: [lang]
  optional: [role]
model: gemma4
```

Invocation example:

```text
/ticket lang=en role=backend Add environment variables and remove hardcoded values from the stats API
```

Supported slash command fields:

- `prompt`: prompt body using Go template variables such as `{{.Input}}`, `{{.lang}}`, `{{.role}}`, and `{{.pwd}}`.
- `template`: alias of `prompt` for compatibility. It uses the same Go template variable syntax.
- `params`: optional params config. Supports legacy `params: [lang]` (all required) and structured mode:

```yaml
params:
  required: [lang]
  optional: [role]
```
- `system`: optional per-command system prompt override for the Ollama request. It also supports the same template variables (`{{.Input}}`, `{{.param_name}}`, `{{.pwd}}`).
- `model`: optional per-command model override.
- `kind`: optional special behavior such as `search` or `config`.

Template variables behavior:

- `{{.Input}}`: remaining free-form user input after parsing named params.
- `{{.param_name}}`: named params declared in `params.required` or `params.optional` (for example `{{.lang}}` or `{{.role}}`).
- `{{.pwd}}`: current working directory where the program was invoked.

If `params` is present, named args are parsed first and the remaining text is passed as `{{.Input}}`. Required params must be provided; optional params can be omitted.

`/search` first asks the model configured in `search_query_model` to rewrite the original prompt into an optimized search query. If Qdrant semantic cache is enabled, it generates an embedding for the query, checks Qdrant for fresh high-score evidence, reranks the hits locally, and answers from cache when the evidence is still valid. If there is no fresh cache hit, it runs the rewritten query against SearXNG, sorts the results by `score`, takes up to 5 sources, downloads each URL, extracts readable content, and sends that material back to the main response model to produce a summary with source links at the end. The original prompt remains the main context for the final answer. If the combined context is too large, the tool first summarizes each source separately and then builds a final summary.

`/config` opens a temporary copy of the active config file in your configured editor. When you save and close, sparkle-cli validates the edited file. If parsing/validation succeeds, it replaces the active config file and hot-reloads runtime configuration without restarting the TUI. If validation fails, the active config is left untouched and the edited temporary file path is shown in the error so you can fix it.

For the full technical flow diagram, see [USER_DOCS/10-search-workflow.md](USER_DOCS/10-search-workflow.md).

`timeout` is kept for backward compatibility and works as a fallback for both flows. If you want to tune them separately, use `search_timeout` for the web phase, `llm_resolve_timeout` for the LLM resolution phase, and `llm_timeout` for the model response.

### Session Logs

Set `logs: true` to enable per-session debug logs.

- Log file name format: `session-[random].log`
- Log location: same directory as the active config file
- Default: `logs: false`

Each request appends timestamped entries including:

- `user_input`
- `model_used`
- `prompt_sent_to_model`
- `system_prompt_sent_to_model`
- `llm_full_response`

## Local Search Stack Example (SearXNG + Qdrant)

A local `docker-compose` example is available at [examples/docker-compose/searxng-qdrant/docker-compose.yml](examples/docker-compose/searxng-qdrant/docker-compose.yml).

Environment defaults for this stack are available at [examples/docker-compose/searxng-qdrant/.env.example](examples/docker-compose/searxng-qdrant/.env.example).

The stack includes:

- `searxng` for metasearch
- `qdrant` for semantic cache

It also includes a minimal SearXNG config file at [examples/docker-compose/searxng-qdrant/settings.yml](examples/docker-compose/searxng-qdrant/settings.yml).

## Development

```bash
go test ./...
```
