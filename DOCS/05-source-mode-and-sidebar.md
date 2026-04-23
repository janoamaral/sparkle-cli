# Source mode and sidebar

[Back to docs index](README.md) | [Previous: Search and semantic cache](04-search-and-semantic-cache.md) | [Next: Interaction modes](06-interaction-modes.md)

After `/search`, you can open and inspect source documents directly in the TUI.

## Enter source mode

1. Press `Ctrl+S` after a `/search` response.
2. Press `1` to `9` to open a source from the last search.

## Layout

- Left pane: readable Markdown content of the selected source.
- Right pane (sidebar): questions and answers about that source.

## Navigation and actions

- `Up` / `Down`: scroll source content (left pane)
- `Shift+Up` / `Shift+Down`: scroll sidebar
- `Ctrl+F`: open in-source search
- `Ctrl+N`: next match
- `Ctrl+Shift+N`: previous match
- `Enter`: ask a question about the opened source
- `Ctrl+S`: return to source list
- `Ctrl+C`: return to normal conversation mode

## Behavior notes

- Source Q&A is grounded on the currently opened source content.
- If the answer is not in the source, the model is instructed to say so clearly.
