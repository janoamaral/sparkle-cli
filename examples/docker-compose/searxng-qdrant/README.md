# SearXNG + Qdrant (docker-compose)

This example starts a local stack for `/search`:

- SearXNG on `http://localhost:8080`
- Qdrant HTTP on `http://localhost:6333`
- Qdrant gRPC on `localhost:6334`

## Start

1. Copy environment defaults:

```bash
cp .env.example .env
```

2. Start the stack:

```bash
docker compose up -d
```

## Stop

```bash
docker compose down
```

## Suggested sparkle-cli config

```yaml
search_url: http://localhost:8080/search
qdrant_enabled: true
qdrant_host: localhost
qdrant_port: 6334
qdrant_use_tls: false
qdrant_collection: semantic_cache
```

Before production usage, update `settings.yml` and replace `server.secret_key`.
