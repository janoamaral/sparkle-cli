# Editor, clipboard, and output acceptance

[Back to docs index](README.md) | [Previous: Interaction modes](06-interaction-modes.md) | [Next: Zsh integration](08-zsh-integration.md)

These features help you move text between the TUI and your shell workflow quickly.

## Edit input in external editor

- Press `Ctrl+E`.
- Your current prompt opens in configured editor.
- On save/close, edited text is loaded back into input.

Supported editors:

- `neovim`
- `vim`
- `vscode`
- `emacs`

## Edit and reload config from TUI

- Run `/config` in the input and press `Enter`.
- sparkle-cli opens a temporary copy of the active config file in your configured editor.
- On save/close, sparkle-cli validates the edited file.
- If valid, it replaces the active config and hot-reloads settings without restarting.
- If invalid, active config is kept unchanged and an error reports where the temp file was preserved.

Behavior on successful `/config` reload:

- It does not add a user message block to conversation history.
- Input is cleared so you can continue with a new command immediately.

## Copy latest assistant response

- Press `Ctrl+Y` to copy the latest assistant response to system clipboard.

## Accept response as command/output

- Press `Ctrl+O`.
- sparkle-cli exits successfully and emits the accepted text.

## `--result-file` flag

You can write accepted output to a file instead of stdout:

```bash
sparkle-cli --result-file /tmp/sparkle-result.txt
```

If the run exits with success and there is accepted output, the file is written with secure permissions.
