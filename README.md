# sparkle-cli

sparkle-cli is a Bubble Tea terminal assistant for shell work. It talks to Ollama over native HTTP, keeps a session in memory, supports slash commands, and can hand a generated command back to Zsh.

## Requirements

- Go 1.22+
- Ollama running locally or reachable through `ollama_url`
- Zsh with ZLE enabled

## Configuration

The config path follows XDG and defaults to `~/.config/sparkle-cli/config.yaml`.

```yaml
ollama_url: http://localhost:11434
model: gemma4
system_prompt: |
  You are a terminal expert. Produce concise, correct shell guidance and prefer returning a single command when the user is asking for one.
timeout: 30
commands:
  explain:
    template: "Explica este comando de forma concisa: {{.Input}}"
  fix:
    template: "Corrige los errores en este comando: {{.Input}}"
  cheat:
    template: "Muestra ejemplos de uso para: {{.Input}}"
```

## Run

```bash
go run ./cmd/sparkle-cli --context "git log --oneline"
```

Key bindings inside the TUI:

- `Enter`: send the current prompt to Ollama
- `Ctrl+O`: accept the latest assistant response and print it to stdout
- `Ctrl+C`: cancel an in-flight request, or exit if idle
- `Esc`: exit without emitting a command

## Zsh Bridge

Source [scripts/init.zsh](/home/logico/ramdisk/sparkle-cli/scripts/init.zsh) from your `.zshrc` after the binary is on `PATH`.

```zsh
source /path/to/sparkle-cli/scripts/init.zsh
```

The widget binds `Ctrl+G`. It captures `$BUFFER`, opens the TUI with `--context`, and only replaces `$BUFFER` when the process exits successfully and emits a non-empty command.

## Slash commands

Slash commands are expanded before the prompt is sent to Ollama.

- `/explain ls -la`
- `/fix kubectl get pods -A --namspace kube-system`
- `/cheat find . -name '*.go'`

## Development

```bash
go test ./...
```