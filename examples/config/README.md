# Configuration Example

Use [config.example.yaml](config.example.yaml) as a baseline.

Suggested flow:

1. Copy the file into your active config path (XDG): `~/.config/sparkle-cli/config.yaml`.
2. Set `ollama_url`, `model`, and search/Qdrant values for your environment.
3. If your Qdrant instance requires auth, export `QDRANT_API_KEY` in your shell environment.
4. If you run Qdrant without TLS locally, keep `qdrant_use_tls: false`.
