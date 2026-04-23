# Zsh integration

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
