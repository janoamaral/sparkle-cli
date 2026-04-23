# Install and first run

## Requirements

- Go 1.22+
- Ollama running locally or remotely
- Optional: Zsh if you want buffer integration

## Run directly from source

```bash
go run ./cmd/sparkle-cli --context "git log --oneline"
```

- `--context` pre-fills the input box with text (for example, your current shell command).

## Basic flow

1. Open the app.
2. Type a question or slash command.
3. Press `Enter` to send.
4. Press `Ctrl+O` to accept the latest assistant response as output.

## Exit behavior

- `Esc`: exits without accepting output.
- `Ctrl+C`: cancels an in-flight request, or exits when idle.

## First useful prompt examples

- `how do I squash the last 3 commits?`
- `/explain grep -R "TODO" .`
- `/generate-code list listening processes on port 3000`
