# Zsh integration

[Back to docs index](README.md) | [Previous: Editor, clipboard, and output](07-editor-clipboard-and-output.md) | [Next: Language and localization](09-language-and-localization.md)

sparkle-cli includes a ZLE widget to bring assistant output back into your shell buffer.

## Setup

Add this to your `.zshrc`:

```zsh
source /path/to/sparkle-cli/scripts/init.zsh
```

## Key binding

- `Ctrl+G` launches sparkle-cli with your current shell buffer as context.

## Buffer replacement behavior

- If sparkle-cli exits successfully and returns non-empty output, your shell buffer is replaced.
- Otherwise, original buffer is preserved.

This makes it safe to test prompts without losing your current command.
