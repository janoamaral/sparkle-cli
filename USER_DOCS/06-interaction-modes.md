# Interaction modes

[Back to docs index](README.md) | [Previous: Source mode and sidebar](05-source-mode-and-sidebar.md) | [Next: Editor, clipboard, and output](07-editor-clipboard-and-output.md)

sparkle-cli supports 3 interaction modes:

- `Normal`
- `Reasoning`
- `Chat`

Switch mode with `Ctrl+T`.

For non-interactive automation, `sparkle-cli direct -m normal "..."` and `sparkle-cli direct -m reasoning "..."` reuse the same two single-turn modes. Direct mode does not support `Chat` and always prints only the final answer to stdout.

## Normal mode

- Standard request/response behavior.
- Good default for most command and troubleshooting prompts.

## Reasoning mode

- Designed for models/flows that emit an explicit thinking channel.
- Useful when you want more visible step-by-step reasoning in the interface.

## Chat mode

- Sends previous user/assistant turns as context on each request.
- Useful for iterative multi-turn problem solving.

## Which one should I use?

- Start in `Normal` for short tasks.
- Use `Chat` for ongoing conversations that need memory.
- Use `Reasoning` when your model setup benefits from explicit thought traces.
