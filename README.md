# LLM0 Gateway

[![Go](https://img.shields.io/badge/Go-1.24-blue?logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Docker](https://img.shields.io/badge/Docker-Compose-blue?logo=docker)](docker-compose.yml)

A production-grade, self-hosted LLM gateway written in Go. One OpenAI-compatible API endpoint for OpenAI, Anthropic, and Google Gemini — with automatic failover, two-tier caching, streaming, rate limiting, and cost tracking out of the box.

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer llm0_live_..." \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hello!"}]}'
```

Switch `gpt-4o-mini` for `claude-haiku-4-5-20251001` or `gemini-2.0-flash` — same endpoint, no code changes in your application.

---

## Features

### Multi-Provider Routing
Route to **OpenAI**, **Anthropic**, and **Google Gemini** through a single OpenAI-compatible API. The gateway detects the correct provider from the model name automatically.

### Automatic Failover
When a provider returns `429 Too Many Requests`, a `5xx` server error, a timeout, or a connection failure, the gateway transparently retries the next provider in the chain — without the caller knowing. Preset chains are defined for all major models.

```
gpt-4o-mini  →  OpenAI (primary)
             →  Anthropic claude-haiku (fallback 1)
             →  Google gemini-2.5-flash (fallback 2)
```

### Two-Tier Caching
- **Exact-match**: SHA-256 cache key checked in Redis first (<1ms), then Postgres (~5ms). Identical requests never hit the LLM twice.
- **Semantic cache**: `pgvector` cosine similarity search detects paraphrased duplicates. Uses `all-MiniLM-L6-v2` (384-dim) embeddings via a bundled Python service.

### Streaming (SSE)
Full Server-Sent Events support for all three providers. Responses are normalized to a single format regardless of which provider is used. Each stream ends with a metadata frame containing cost and usage:

```
data: {"cost_usd":0.0000021,"latency_ms":1371,"object":"chat.completion.chunk.metadata",...}
data: [DONE]
```

### Token Bucket Rate Limiting
Per-API-key request limits enforced atomically via Redis Lua scripts. Configurable `rate_limit_per_minute` per key.

### Per-Customer Spend Caps
Pass `X-Customer-ID` on any request to enable per-customer daily and monthly USD spend limits. Supports two behaviors when a limit is reached:
- `block` — return `429` with spend details
- `downgrade` — automatically route to a cheaper model

### Cost Tracking
Pre-request cost estimation (for spend cap checks) plus post-request reconciliation based on actual token usage. Costs are pulled from the `model_pricing` table and stored per request.

### Request Logging
Every request is logged to `gateway_logs` with: provider, model, tokens, cost, latency, cache status, failover info, and optional customer labels.

### Background Workers
- **Cache cleanup**: removes expired exact-match and semantic cache entries
- **Spend reconciliation**: syncs Redis counters to Postgres periodically
- **Log maintenance**: manages log retention

---

## Supported Models

### OpenAI
| Model | Tier |
|---|---|
| `gpt-4o` | Flagship |
| `gpt-4o-mini` | Cost-optimized |
| `gpt-4-turbo` | Flagship |
| `gpt-3.5-turbo` | Budget |

### Anthropic
| Model | Tier |
|---|---|
| `claude-opus-4-6` | Most capable |
| `claude-sonnet-4-6` | Balanced |
| `claude-opus-4-5-20251101` | Most capable |
| `claude-sonnet-4-5-20250929` | Balanced |
| `claude-haiku-4-5-20251001` | Cost-optimized |
| `claude-opus-4-20250514` | Most capable |
| `claude-sonnet-4-20250514` | Balanced |
| `claude-3-haiku-20240307` | Budget |

### Google Gemini
| Model | Tier |
|---|---|
| `gemini-2.5-pro` | Most capable |
| `gemini-2.5-flash` | Balanced |
| `gemini-2.0-flash` | Cost-optimized |
| `gemini-2.0-flash-lite` | Budget |

---

## Quick Start

### Option A — Docker Compose (recommended)

Requires: [Docker Desktop](https://www.docker.com/products/docker-desktop/).

**Step 1 — Clone and configure**

```bash
git clone https://github.com/mrmushfiq/llm0-gateway
cd llm0-gateway

cp .env.example .env
```

Open `.env` and add at least one provider API key:

```env
OPENAI_API_KEY=sk-proj-...
ANTHROPIC_API_KEY=sk-ant-...
GEMINI_API_KEY=AIza...
```

**Step 2 — Build the images**

```bash
docker compose build
```

> **This takes 3–5 minutes on first run.** The embedding service downloads and bakes the `all-MiniLM-L6-v2` model weights (~90MB) into the image at build time so startup is instant afterwards. Subsequent builds use the Docker layer cache and complete in seconds.

**Step 3 — Start all services**

```bash
docker compose up
```

Postgres (with `pgvector`), Redis, the embedding service, and the gateway all start together. The database schema is applied automatically on first boot. When you see:

```
llm0_gateway  | ✅ Failover executor initialized with 3 providers
llm0_gateway  | ✅ Semantic cache initialized
llm0_gateway  | 🚀 LLM0 Gateway listening on :8080
```

the gateway is ready.

**Step 4 — Create an API key**

```bash
./scripts/create_api_key.sh
```

**Step 5 — Send your first request**

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer llm0_live_..." \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Say hello!"}]}'
```

**Useful Docker commands**

```bash
# Run in background
docker compose up -d

# View gateway logs
docker compose logs -f gateway

# Stop everything
docker compose down

# Stop and wipe all data (full reset)
docker compose down -v

# Restart just the gateway (e.g. after editing .env)
docker compose up -d gateway
```

### Option B — Run with Go

Requires: Go 1.24+, Postgres with the `pgvector` extension, Redis.

**Step 1 — Clone and configure**

```bash
git clone https://github.com/mrmushfiq/llm0-gateway
cd llm0-gateway

cp .env.example .env
# Edit .env — set DATABASE_URL, REDIS_URL, and at least one provider key
```

**Step 2 — Apply the database schema**

```bash
psql $DATABASE_URL -f schema/schema.sql
```

**Step 3 — (Optional) Start the embedding service for semantic caching**

```bash
cd embedding_service
pip install -r requirements.txt
uvicorn app:app --host 0.0.0.0 --port 8001
```

Then set `EMBEDDING_SERVICE_URL=http://localhost:8001` in your `.env`. Skip this step to run without semantic caching — exact-match caching still works.

**Step 4 — Run the gateway**

```bash
go run ./cmd/gateway/main.go
```

Or build a binary:

```bash
go build -o llm0-gateway ./cmd/gateway/main.go
./llm0-gateway
```

---

## Creating Your First API Key

API keys are in the format `llm0_live_<64 hex chars>`. Only the `bcrypt(SHA-256(key))` hash is stored — the raw key is shown once.

The script requires Docker Compose to be running (uses `pgcrypto` inside Postgres — no host dependencies needed):

```bash
./scripts/create_api_key.sh
```

Example output:

```
════════════════════════════════════════════════
  LLM0 Gateway — Create API Key
════════════════════════════════════════════════

▶  Generated key (save this — shown only once):

   llm0_live_c0244eec5b7a8426a6a96b5f9748efa8...

▶  Bcrypt hash generated (via pgcrypto)
▶  Project ID : 54ce26a8-2f93-4afd-924d-28a8832ea52e
▶  Key prefix : llm0_live_c0244...

  Test it:

  curl http://localhost:8080/v1/chat/completions \
    -H "Authorization: Bearer llm0_live_c0244..." \
    ...
════════════════════════════════════════════════
```

---

## Configuration

All configuration is via environment variables. Copy `.env.example` to `.env`.

| Variable | Required | Default | Description |
|---|---|---|---|
| `OPENAI_API_KEY` | At least one | — | OpenAI API key |
| `ANTHROPIC_API_KEY` | At least one | — | Anthropic API key |
| `GEMINI_API_KEY` | At least one | — | Google Gemini API key |
| `DATABASE_URL` | Yes | — | Postgres connection string (must have `pgvector` extension) |
| `REDIS_URL` | Yes | — | Redis connection string |
| `PORT` | No | `8080` | Gateway listen port |
| `ENVIRONMENT` | No | `local` | `local` or `production` |
| `CACHE_TTL_SECONDS` | No | `3600` | Exact-match cache TTL in seconds |
| `EMBEDDING_SERVICE_URL` | No | `""` | Enables semantic caching when set. Docker Compose sets this automatically. |

---

## How It Works

### Request Pipeline

```
Incoming Request
        │
        ▼
  Auth Middleware          validate Bearer token (bcrypt verify, Redis-cached)
        │
        ▼
  Rate Limit Check         token bucket per API key via atomic Redis Lua script
        │
        ▼
  Spend Cap Check          block if project monthly_cap_usd exceeded
        │
        ▼
  Exact-Match Cache        SHA-256 key → Redis (<1ms) → Postgres (~5ms)
        │  cache hit: return immediately
        ▼
  Semantic Cache           pgvector cosine similarity search (~20–50ms)
        │  cache hit: return immediately
        ▼
  Customer Limit Check     per X-Customer-ID daily/monthly spend cap
        │
        ▼
  Provider Call            OpenAI / Anthropic / Gemini
        │  on 429/5xx/timeout → automatic failover to next provider
        ▼
  Response                 streaming SSE or non-streaming JSON
        │
        ▼
  Async Workers            log request, update spend counters, store in cache
```

### Failover Chains

All preset chains are defined in `internal/gateway/failover/chains.go`. Each model has a 3-step chain across different providers.

| Requested Model | Step 1 | Step 2 | Step 3 |
|---|---|---|---|
| `gpt-4o` | OpenAI | Anthropic claude-sonnet-4-6 | Google gemini-2.5-pro |
| `gpt-4o-mini` | OpenAI | Anthropic claude-haiku-4-5 | Google gemini-2.5-flash |
| `claude-sonnet-4-6` | Anthropic | OpenAI gpt-4o | Google gemini-2.5-pro |
| `claude-haiku-4-5-20251001` | Anthropic | OpenAI gpt-4o-mini | Google gemini-2.5-flash |
| `gemini-2.5-pro` | Google | OpenAI gpt-4o | Anthropic claude-sonnet-4-6 |
| `gemini-2.5-flash` | Google | OpenAI gpt-4o-mini | Anthropic claude-haiku-4-5 |

Failover triggers on: `429`, `5xx`, connection timeout, connection error, `401`/`403` (auth failure — next provider may have a valid key), `404` (model not available on that provider).

### Exact-Match Cache

Cache key: `SHA-256(project_id + provider + model + sorted_messages_json)`

Two-tier lookup:
1. **Redis** (hot) — sub-millisecond, in-memory
2. **Postgres** (warm) — ~5ms, survives Redis restarts

Cache hits cost `$0.00` and are returned in `<1ms`.

### Semantic Cache

When `EMBEDDING_SERVICE_URL` is configured, the first user message is embedded using `all-MiniLM-L6-v2` (384 dimensions). The embedding is compared against stored vectors in Postgres using `pgvector` cosine similarity.

```
Gateway ──POST /embed──► Embedding Service (all-MiniLM-L6-v2, CPU)
        ◄─[0.12, -0.34, ...]──

        ──cosine similarity──► pgvector (threshold: 0.95)
```

Cache hits return the stored response without any LLM API call.

**Threshold**: configurable per project (`semantic_threshold` column, default `0.95`). Lower values return more matches but risk returning less relevant cached responses.

---

## Response Headers

Every response includes diagnostic headers:

| Header | Description |
|---|---|
| `X-Cache-Hit` | `exact`, `semantic`, or `miss` |
| `X-Cache-Similarity` | Cosine similarity score (semantic hits only) |
| `X-Provider` | Which provider served the response |
| `X-Cost-USD` | Actual cost of the request |
| `X-Tokens-Prompt` | Prompt token count |
| `X-Tokens-Completion` | Completion token count |
| `X-RateLimit-Remaining` | Requests remaining in current window |
| `X-Failover` | `true` if failover occurred |
| `X-Original-Provider` | Provider that was tried first (on failover) |

---

## Per-Customer Spend Limits

Set limits per end-user via the `customer_limits` table:

```sql
INSERT INTO customer_limits (
    project_id, customer_id,
    daily_spend_limit_usd, monthly_spend_limit_usd,
    on_limit_behavior, downgrade_model
) VALUES (
    '<your-project-id>',
    'user_123',
    1.00,          -- $1 per day
    20.00,         -- $20 per month
    'downgrade',   -- 'block' or 'downgrade'
    'gpt-4o-mini'  -- used when on_limit_behavior = 'downgrade'
);
```

Then pass the customer ID on requests:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer llm0_live_..." \
  -H "X-Customer-ID: user_123" \
  ...
```

Spend headers are included in every response:
- `X-Customer-Spend-Today`
- `X-Customer-Limit-Daily`
- `X-Customer-Remaining-Usd`

---

## Performance

End-to-end latency measured locally:

| Scenario | Latency |
|---|---|
| Exact-match cache hit (Redis) | **<1ms** |
| Exact-match cache hit (Postgres) | **~5ms** |
| Semantic cache hit | **~20–50ms** |
| Pass-through (no cache) | Provider latency + **<5ms** gateway overhead |

Gateway processing overhead (auth + rate limit + cache check + response): **<5ms** at low concurrency. The single Go binary has minimal memory footprint (~30MB idle).

---

## Endpoints

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/v1/chat/completions` | Bearer token | Chat completions — streaming and non-streaming |
| `GET` | `/health` | None | Basic liveness check |
| `GET` | `/ready` | None | Readiness check (Postgres + Redis connectivity) |
| `GET` | `/live` | None | Liveness check |

---

## Project Structure

```
llm0-gateway/
├── cmd/gateway/main.go              # Entry point, router setup, worker initialization
├── internal/
│   ├── gateway/
│   │   ├── auth/                   # API key validation (bcrypt + Redis cache)
│   │   ├── cache/                  # Exact-match (Redis+Postgres) and semantic cache
│   │   ├── cost/                   # Pre/post request cost calculation
│   │   ├── embeddings/             # HTTP client for embedding service
│   │   ├── failover/               # Failover executor + preset model chains
│   │   ├── handlers/               # Gin HTTP handlers (chat, streaming, health)
│   │   ├── providers/              # OpenAI, Anthropic, Gemini provider clients
│   │   ├── ratelimit/              # Per-API-key and per-customer rate limiting
│   │   ├── streaming/              # SSE normalization across providers
│   │   └── workers/                # Background jobs (cache GC, reconciliation)
│   └── shared/
│       ├── config/                 # Environment variable loader
│       ├── database/               # Postgres connection pool + query helpers
│       ├── models/                 # Shared Go structs (Project, APIKey, etc.)
│       ├── redis/                  # Redis client with rate limit + spend cap logic
│       └── tls/                    # TLS 1.3 config
├── embedding_service/
│   ├── app.py                      # FastAPI embedding server
│   ├── requirements.txt
│   └── Dockerfile                  # Bakes all-MiniLM-L6-v2 weights at build time
├── schema/schema.sql               # Canonical DB schema (single source of truth)
├── scripts/
│   └── create_api_key.sh           # Project + API key creation helper
├── docker-compose.yml              # Postgres, Redis, embedding service, gateway
├── Dockerfile
└── .env.example
```

---

## Architecture

```
                        ┌─────────────────────────────┐
                        │         LLM0 Gateway         │
                        │         (Go, :8080)           │
                        └──────────────┬───────────────┘
                                       │
               ┌───────────────────────┼───────────────────────┐
               │                       │                       │
               ▼                       ▼                       ▼
       ┌──────────────┐      ┌──────────────────┐    ┌──────────────────┐
       │    Redis     │      │    PostgreSQL     │    │ Embedding Service │
       │  Rate limits │      │  API keys, logs  │    │ all-MiniLM-L6-v2 │
       │  Exact cache │      │  Exact cache     │    │  (Python, :8080) │
       │  Spend totals│      │  Semantic cache  │    └──────────────────┘
       └──────────────┘      │  Model pricing   │
                             └──────────────────┘
                                       │
               ┌───────────────────────┼───────────────────────┐
               │                       │                       │
               ▼                       ▼                       ▼
       ┌──────────────┐      ┌──────────────────┐    ┌──────────────────┐
       │    OpenAI    │      │    Anthropic      │    │  Google Gemini   │
       └──────────────┘      └──────────────────┘    └──────────────────┘
```

---

## Contributing

Contributions are welcome. Please open an issue before submitting large changes.

Areas where contributions are especially useful:
- Additional provider support (Mistral, Cohere, AWS Bedrock, Azure OpenAI)
- Admin API for key and project management
- Prometheus metrics endpoint
- Dashboard for request analytics

---

## License

MIT — see [LICENSE](LICENSE).
