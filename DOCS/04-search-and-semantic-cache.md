# Search and semantic cache

[Back to docs index](README.md) | [Previous: Slash commands](03-slash-commands.md) | [Next: Source mode and sidebar](05-source-mode-and-sidebar.md)

`/search` is a special slash command designed for evidence-based answers.

## What `/search` does

1. Rewrites your original query into a web-search-optimized query.
2. Tries semantic cache first (if Qdrant is enabled).
3. If cache misses, runs web search and downloads source pages.
4. Extracts readable content and keeps the most relevant chunks.
5. Builds an answer grounded on those sources.
6. Returns source links with the response.

The final answer is generated in the same dominant language as your original query.

## Qdrant cache-first mode

Enable in config:

```yaml
qdrant_enabled: true
qdrant_host: qdrant.nest.com.ar
qdrant_port: 6334
qdrant_api_key: ${QDRANT_API_KEY}
qdrant_use_tls: true
qdrant_collection: semantic_cache
qdrant_score_threshold: 0.90
qdrant_ttl_hours: 48
qdrant_pool_size: 3
```

When enabled:

- sparkle-cli searches semantic cache first.
- On high-quality fresh hit, it can answer from cache immediately.
- On miss, it falls back to web search and ingests new evidence in background.

## Practical tips

- Use specific queries to improve source quality.
- If results look weak, refine your question and re-run `/search`.
- If a result lacks citation markers, sparkle-cli still appends a source footer when possible.
