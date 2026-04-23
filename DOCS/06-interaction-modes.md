# Interaction modes

sparkle-cli supports 3 interaction modes:

- `Normal`
- `Reasoning`
- `Chat`

Switch mode with `Ctrl+T`.

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
