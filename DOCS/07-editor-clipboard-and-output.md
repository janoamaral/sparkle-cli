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
