# LLM0 Gateway

[![Go](https://img.shields.io/badge/Go-1.24-blue?logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![Docker](https://img.shields.io/badge/Docker-Compose-blue?logo=docker)](docker-compose.yml)
[![OpenAI Compatible](https://img.shields.io/badge/API-OpenAI_Compatible-412991)](https://platform.openai.com/docs/api-reference)

A production-grade, self-hosted LLM gateway written in Go. One **OpenAI-compatible** API endpoint for **OpenAI**, **Anthropic**, **Google Gemini**, and **local Ollama models** — with configurable cloud/local failover, two-tier caching, streaming, per-key rate limiting, per-customer spend caps, and cost tracking out of the box.

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer llm0_live_..." \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hello!"}]}'
```

Switch `gpt-4o-mini` for `claude-haiku-4-5-20251001`, `gemini-2.0-flash`, or any local Ollama model (`llama3.3`, `qwen2.5`, `gemma3`, …) — same endpoint, no code changes in your application.

---

## Why LLM0 Gateway?

- **One endpoint, four backends** — swap between OpenAI, Anthropic, Gemini, and local Ollama models without touching client code.
- **Local-first or cloud-first, your choice** — a single `FAILOVER_MODE` env var decides whether requests try Ollama first, cloud first, local-only, or cloud-only. Great for privacy-sensitive workloads that need cloud as a backup.
- **Never get paged for a provider outage** — automatic failover on `429`/`5xx`/`4xx`/timeout/connection errors across providers. Clients never see the failure.
- **Save real money** — exact-match cache returns `<1ms`, semantic cache catches paraphrased duplicates, and local Ollama calls cost `$0`.
- **Built-in SaaS controls** — per-API-key rate limits, per-customer spend caps, hard monthly project caps, customer labels for analytics.
- **Zero lock-in** — single Go binary, standard Postgres + Redis, open source.

---

## Features

### Multi-Provider Routing
Route to **OpenAI**, **Anthropic**, **Google Gemini**, and **Ollama** (local models) through a single OpenAI-compatible API. The gateway detects the correct provider from the model name automatically and exposes a standard `GET /v1/models` endpoint for SDK discovery.

### Configurable Failover Modes
Set `FAILOVER_MODE` to control how cloud and local providers are ordered in the failover chain:

| Mode | Behavior | Typical use case |
|---|---|---|
| `cloud_first` *(default)* | Cloud providers first, Ollama as last-resort fallback | Production, best quality + cost reduction |
| `local_first` | Ollama first, cloud as fallback when local fails | Privacy-first apps, air-gapped + cloud-capable |
| `local_only` | Never contact cloud APIs | Offline, compliance, dev without API keys |
| `cloud_only` | Never use local models (even if configured) | Pure cloud deployments |

### Automatic Cross-Provider Failover
When a provider returns `429`, `5xx`, `401`/`403`, `404`, a timeout, or a connection failure, the gateway transparently retries the next provider in the chain — without the caller knowing. Preset chains are defined for all major models.

```
gpt-4o-mini  →  OpenAI (primary)
             →  Anthropic claude-haiku-4-5
             →  Google gemini-2.5-flash
             →  Ollama qwen2.5:14b   (if OLLAMA_BASE_URL is set)
```

Response headers `X-Failover: true` and `X-Original-Provider` tell you when a failover happened.

### Local Ollama Support
Point the gateway at a running Ollama instance (`OLLAMA_BASE_URL=http://host.docker.internal:11434/v1`) and:
- All pulled Ollama models become routable through `/v1/chat/completions`
- They appear automatically in `GET /v1/models`
- Streaming works identically to cloud providers
- Cost is always `$0` — skipped in spend checks and logs
- Tier mapping (`OLLAMA_MODEL_FLAGSHIP`, `_BALANCED`, `_BUDGET`) transparently substitutes local models for cloud equivalents during failover

### Two-Tier Caching
- **Exact-match**: SHA-256 cache key checked in Redis first (<1ms), then Postgres (~5ms). Identical requests never hit the LLM twice.
- **Semantic cache**: `pgvector` cosine similarity search detects paraphrased duplicates. Uses `all-MiniLM-L6-v2` (384-dim) embeddings via a bundled Python service.

Both caches are toggleable per API key (`cache_enabled`, `semantic_cache_enabled` columns).

### Streaming (SSE)
Full Server-Sent Events support for all providers including Ollama. Responses are normalized to a single OpenAI-compatible format regardless of which provider is used. Each stream ends with a metadata frame containing cost and usage:

```
data: {"cost_usd":0.0000021,"latency_ms":1371,"object":"chat.completion.chunk.metadata",...}
data: [DONE]
```

### Token Bucket Rate Limiting (per API key)
Each API key has its own `rate_limit_per_minute` enforced atomically in Redis via Lua scripts — no race conditions under high concurrency. Uses a full token bucket algorithm (not a naive counter), so burst traffic within the minute is allowed as long as the per-minute rate isn't breached.

Response headers included on every call:
- `X-RateLimit-Limit`
- `X-RateLimit-Remaining`
- `X-RateLimit-Reset` (Unix timestamp)

When the limit is exceeded, the gateway returns `429` with a `retry_after` field.

### Per-Customer Spend Caps
Pass `X-Customer-ID` on any request to enable per-end-user daily and monthly USD spend limits. Limits are stored in the `customer_limits` table and support two overflow behaviors:
- `block` — return `429` with spend details and how much longer until reset
- `downgrade` — automatically route to a cheaper model (e.g. `gpt-4o` → `gpt-4o-mini`)

Customer labels (`X-LLM0-Tier: pro`, `X-LLM0-Team: billing`, …) are stored as JSONB on every request log for downstream analytics.

### Hard Project Spend Cap
Set `monthly_cap_usd` on a project and requests are blocked with `402 Payment Required` once the cap is hit. Checked **before** the LLM call using cost estimation, so runaway prompts can't silently exceed the cap.

### Cost Tracking
Pre-request cost estimation (for spend cap checks) plus post-request reconciliation based on actual token usage. Costs are pulled from the `model_pricing` table and stored per request. Local Ollama calls are always `$0`.

### Request Logging
Every request is logged to `gateway_logs` with: provider, model, tokens, cost, latency, cache status (exact/semantic/miss), similarity score, failover info, customer ID, and arbitrary labels.

### Background Workers
- **Cache cleanup** — removes expired exact-match and semantic cache entries
- **Spend reconciliation** — syncs Redis counters to Postgres periodically
- **Log maintenance** — manages log retention

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

### Ollama (local)
Any model pulled on your Ollama instance is automatically routable — `llama3.3:70b`, `qwen2.5:14b`, `gemma3:4b`, `mistral`, `deepseek-r1`, etc. Pull models with `ollama pull <model>` and they appear in `GET /v1/models` instantly.

The tier env vars (`OLLAMA_MODEL_FLAGSHIP`, `OLLAMA_MODEL_BALANCED`, `OLLAMA_MODEL_BUDGET`) tell the failover engine which local model to substitute when a cloud model is requested. For example, with `OLLAMA_MODEL_BALANCED=qwen2.5:14b` set, a `gpt-4o-mini` request in `local_first` mode tries `qwen2.5:14b` first, then `gpt-4o-mini` on OpenAI if the local call fails.

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

**Step 6 — (Optional) Add local Ollama models**

If you're running [Ollama](https://ollama.com) on your host machine, point the gateway at it for local, zero-cost inference with cloud failover:

```env
# In .env
OLLAMA_BASE_URL=http://host.docker.internal:11434/v1
FAILOVER_MODE=local_first

# Map local models to tiers (match whatever you've pulled)
OLLAMA_MODEL_FLAGSHIP=llama3.3:70b
OLLAMA_MODEL_BALANCED=qwen2.5:14b
OLLAMA_MODEL_BUDGET=gemma3:4b
```

Then restart the gateway and test:

```bash
docker compose up -d --force-recreate gateway

# Request a cloud model — gets served by Ollama first, cloud as fallback
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer llm0_live_..." \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hello!"}]}'

# List everything the gateway can route (cloud + local)
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer llm0_live_..."
```

The `X-Provider` response header shows which backend actually served the request.

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

## Managing Model Pricing

### How the default list is seeded

The gateway ships with a curated set of model prices in [`schema/seed_models.sql`](schema/seed_models.sql). It's applied automatically on **first** Postgres boot via the `docker-entrypoint-initdb.d/` mount.

- **Docker Compose users (fresh install)** — no action needed. Works out of the box.
- **Docker Compose users (existing install)** — Postgres only runs initdb scripts on an empty data volume, so an upgraded `seed_models.sql` won't auto-apply. Re-run it manually against your live DB (safe — idempotent):
  ```bash
  docker compose exec -T postgres psql -U llm0 -d llm0_gateway \
    -f /docker-entrypoint-initdb.d/02_seed_models.sql
  ```
- **Non-Docker / manual Postgres** — after applying `schema/schema.sql`, also run:
  ```bash
  psql $DATABASE_URL -f schema/seed_models.sql
  ```

The seed uses `ON CONFLICT (provider, model) DO NOTHING`, so it's safe to re-run and will never overwrite rows you've managed manually.

> **Want stricter schema versioning?** The project ships a single `schema.sql` + `seed_models.sql` pair for simplicity. If your team prefers versioned, reversible migrations, drop in [`golang-migrate`](https://github.com/golang-migrate/migrate) (classic up/down SQL files) or [Atlas](https://atlasgo.io/) (declarative, diff-based) — both integrate cleanly without changing application code.

### Adding / updating / removing entries

Model prices live in the `model_pricing` table. Use the bundled interactive script to add, update, or delete entries when providers release new models or change prices:

```bash
./scripts/manage_models.sh           # interactive menu
./scripts/manage_models.sh list      # list all models
./scripts/manage_models.sh add       # add a new model
./scripts/manage_models.sh update    # update pricing for an existing model
./scripts/manage_models.sh delete    # remove a model
```

After any change, restart the gateway to reload the pricing cache:

```bash
docker compose restart gateway
```

Prices are specified per 1,000 tokens in USD (e.g. `gpt-4o-mini` input is `0.00015`). Ollama models can be added with `0.00000000` prices to make their cost explicit in request logs.

### Keeping pricing current

Provider pricing drifts — new models launch, old ones get cheaper, and context windows change. Here's the policy:

| Situation | What to do |
|---|---|
| New model released upstream | Add it with `./scripts/manage_models.sh add` — no code change needed. Cloud providers are routed by prefix (`gpt-*`, `claude-*`, `gemini-*`), so new models work immediately. |
| Want the fix to persist across fresh installs | Submit a PR updating [`schema/seed_models.sql`](schema/seed_models.sql). That single file is the canonical source of truth. |
| Pricing changed on an existing model | `./scripts/manage_models.sh update` locally; PR the seed file for the upstream fix. |
| Running a fleet of gateways | Roll out the updated `seed_models.sql` and apply it once per database (`psql ... -f seed_models.sql`). It's idempotent, so re-running is safe. |

We intentionally **do not** auto-scrape provider pricing pages: those pages are unstable, ToS-ambiguous, and silently reformat. Community-reviewed PRs against `seed_models.sql` are the safest long-term update channel — the same approach LiteLLM uses.

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

### Required

| Variable | Description |
|---|---|
| `DATABASE_URL` | Postgres connection string (must have `pgvector` extension) |
| `REDIS_URL` | Redis connection string |
| At least one of: `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`, `OLLAMA_BASE_URL` | The gateway routes to whichever providers have keys set |

### Cloud Providers

| Variable | Default | Description |
|---|---|---|
| `OPENAI_API_KEY` | — | OpenAI API key |
| `ANTHROPIC_API_KEY` | — | Anthropic API key |
| `GEMINI_API_KEY` | — | Google Gemini API key |

### Local Models (Ollama)

| Variable | Default | Description |
|---|---|---|
| `OLLAMA_BASE_URL` | `""` | Set to enable local models. In Docker: `http://host.docker.internal:11434/v1`. Native: `http://localhost:11434/v1` |
| `OLLAMA_MODEL_FLAGSHIP` | `llama3.3:70b` | Local model used as substitute for flagship-tier cloud models (gpt-4o, claude-opus, gemini-pro) |
| `OLLAMA_MODEL_BALANCED` | `qwen2.5:14b` | Local model used as substitute for balanced-tier cloud models (gpt-4o-mini, claude-sonnet, gemini-flash) |
| `OLLAMA_MODEL_BUDGET` | `gemma3:4b` | Local model used as substitute for budget-tier cloud models (gpt-3.5, claude-haiku, gemini-flash-lite) |

### Failover

| Variable | Default | Description |
|---|---|---|
| `FAILOVER_MODE` | `cloud_first` | One of `cloud_first`, `local_first`, `local_only`, `cloud_only`. See [Failover Modes](#configurable-failover-modes) above |

### Server & Infrastructure

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | Gateway listen port |
| `ENVIRONMENT` | `local` | `local` or `production` (switches Gin to release mode) |
| `CACHE_TTL_SECONDS` | `3600` | Exact-match cache TTL in seconds |
| `EMBEDDING_SERVICE_URL` | `""` | Enables semantic caching when set. Docker Compose sets this automatically |
| `REQUEST_TIMEOUT` | `30s` | Upstream request timeout |
| `MAX_CONCURRENT_REQUESTS` | `10000` | Concurrency ceiling for the HTTP server |

### TLS (optional)

| Variable | Default | Description |
|---|---|---|
| `TLS_ENABLED` | `false` | Enable TLS 1.3 |
| `TLS_CERT_FILE` | — | Path to certificate |
| `TLS_KEY_FILE` | — | Path to private key |

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

Failover chains are **dynamically composed at request time** based on `FAILOVER_MODE` and whether Ollama is configured. The base cloud chains are defined in `internal/gateway/failover/chains.go`.

**Base cloud chains** (used when no Ollama is configured, or in `cloud_only` mode):

| Requested Model | Step 1 | Step 2 | Step 3 |
|---|---|---|---|
| `gpt-4o` | OpenAI | Anthropic claude-sonnet-4-6 | Google gemini-2.5-pro |
| `gpt-4o-mini` | OpenAI | Anthropic claude-haiku-4-5 | Google gemini-2.5-flash |
| `claude-sonnet-4-6` | Anthropic | OpenAI gpt-4o | Google gemini-2.5-pro |
| `claude-haiku-4-5-20251001` | Anthropic | OpenAI gpt-4o-mini | Google gemini-2.5-flash |
| `gemini-2.5-pro` | Google | OpenAI gpt-4o | Anthropic claude-sonnet-4-6 |
| `gemini-2.5-flash` | Google | OpenAI gpt-4o-mini | Anthropic claude-haiku-4-5 |

**Effect of `FAILOVER_MODE`** (example: request for `gpt-4o-mini` with `OLLAMA_MODEL_BALANCED=qwen2.5:14b`):

| Mode | Resulting chain |
|---|---|
| `cloud_only` | OpenAI → Anthropic haiku → Gemini flash |
| `cloud_first` | OpenAI → Anthropic haiku → Gemini flash → Ollama qwen2.5:14b |
| `local_first` | Ollama qwen2.5:14b → OpenAI → Anthropic haiku → Gemini flash |
| `local_only` | Ollama qwen2.5:14b |

**Tier resolution** — the gateway chooses which Ollama model to substitute based on the cloud model's quality tier: flagship (gpt-4o, claude-opus, gemini-pro), balanced (gpt-4o-mini, claude-sonnet, gemini-flash), or budget (gpt-3.5, claude-haiku, gemini-flash-lite).

**Failover triggers**: `429` (rate limit), `5xx` (server error), connection timeout, connection error, `401`/`403` (auth failure — next provider may have a valid key), `404` (model not available on that provider).

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

## Rate Limiting & Cost Controls

The gateway has **three independent layers** of usage control, evaluated in order on every request:

### 1. Per-API-Key Rate Limit (requests/minute)
A token-bucket algorithm runs atomically in Redis via a Lua script — no race conditions even under thousands of concurrent calls. Each API key has its own `rate_limit_per_minute` stored in the `api_keys` table.

```sql
-- Set a key to 120 requests per minute
UPDATE api_keys SET rate_limit_per_minute = 120 WHERE key_prefix = 'llm0_live_abc12';
```

The client sees:
- `X-RateLimit-Limit` — the bucket capacity
- `X-RateLimit-Remaining` — tokens left in the current window
- `X-RateLimit-Reset` — Unix timestamp when the bucket refills
- `429` with `retry_after` when exceeded

### 2. Hard Project Spend Cap (USD/month)
Each project has a `monthly_cap_usd` column. The gateway **estimates** the request cost before calling the LLM; if it would push the project over the cap, the request is blocked with `402 Payment Required`. This prevents runaway prompts from silently burning dollars.

```sql
-- Set a $50/month cap for a project
UPDATE projects SET monthly_cap_usd = 50.00 WHERE id = '<project-id>';
```

### 3. Per-Customer Spend Limits (daily + monthly USD)
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

### How Cost is Calculated

The gateway tracks cost in two places: **before** the call (for spend-cap enforcement) and **after** the call (for actual billing).

**1. Pricing source** — the `model_pricing` table, one row per `(provider, model)` pair with `input_per_1k_tokens` and `output_per_1k_tokens`. Pricing is loaded into memory at startup — restart the gateway after updates via `./scripts/manage_models.sh`.

**2. Cost formula** — applied identically in every path:

```
cost_usd = (input_tokens  / 1000) × input_per_1k_tokens
         + (output_tokens / 1000) × output_per_1k_tokens
```

Both input and output prices are always applied. Ollama (local) requests are always `$0`, regardless of token counts.

**3. Pre-request estimation** — used to block requests that would breach a project or customer spend cap *before* any API call is made:

- **Input tokens** are estimated as `sum(len(role) + len(content) + 4) / 4` across all messages (the industry-standard "~4 chars per token" heuristic).
- **Output tokens** use the client-supplied `max_tokens` if present. If not, defaults to `2 × input_tokens` clamped to `[100, 2000]` so neither tiny nor huge prompts produce wildly skewed estimates.

This means clients can send `max_tokens: 500` to get a tight, accurate pre-estimate — useful when hovering near a spend cap.

**4. Post-request actual cost** — the gateway reads real `prompt_tokens` and `completion_tokens` from the provider's response and recalculates, then reconciles the difference against Redis spend counters. Every request log in `gateway_logs` has the real cost.

### Cost Tracking Example

```bash
# Make a request
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer llm0_live_..." \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "What is F1?"}],
    "max_tokens": 200
  }'
```

The response headers tell you:
- `X-Cost-USD: 0.000110`
- `X-Tokens-Prompt: 15`
- `X-Tokens-Completion: 180`

Aggregate spend by customer, model, or day:

```sql
-- Top 10 costliest customers this month
SELECT customer_id, SUM(cost_usd) AS total, COUNT(*) AS requests
FROM gateway_logs
WHERE created_at >= date_trunc('month', NOW())
GROUP BY customer_id
ORDER BY total DESC
LIMIT 10;

-- Spend breakdown by model
SELECT model, SUM(cost_usd) AS total, SUM(tokens_total) AS tokens
FROM gateway_logs
WHERE created_at >= NOW() - INTERVAL '7 days'
GROUP BY model
ORDER BY total DESC;

-- Average cost per request by customer tier (from labels)
SELECT labels->>'Tier' AS tier, AVG(cost_usd) AS avg_cost
FROM gateway_logs
WHERE labels ? 'Tier'
GROUP BY tier;
```

### Customer Labels
Attach arbitrary labels to any request for analytics — they're stored as JSONB on `gateway_logs`:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "X-Customer-ID: user_123" \
  -H "X-LLM0-Tier: pro" \
  -H "X-LLM0-Team: billing" \
  ...
```

Query the logs later:
```sql
SELECT labels->>'Tier', SUM(cost_usd) FROM gateway_logs GROUP BY 1;
```

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
| `GET` | `/v1/models` | Bearer token | OpenAI-compatible model list (includes cloud + pulled Ollama models) |
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
       │    Redis     │      │    PostgreSQL    │    │ Embedding Service │
       │  Rate limits │      │  API keys, logs  │    │ all-MiniLM-L6-v2 │
       │  Exact cache │      │  Exact cache     │    │   (Python)       │
       │  Spend totals│      │  Semantic cache  │    └──────────────────┘
       └──────────────┘      │  Model pricing   │
                             └──────────────────┘
                                       │
         ┌─────────────────┬───────────┴──────────────┬─────────────────┐
         │                 │                          │                 │
         ▼                 ▼                          ▼                 ▼
 ┌──────────────┐  ┌──────────────┐          ┌──────────────┐  ┌──────────────┐
 │    OpenAI    │  │   Anthropic  │          │ Google Gemini│  │    Ollama    │
 │              │  │              │          │              │  │   (local)    │
 └──────────────┘  └──────────────┘          └──────────────┘  └──────────────┘
                  ◄── cloud providers ──►                      ◄── optional ──►
```

---

## Contributing

Contributions are welcome. Please open an issue before submitting large changes.

Areas where contributions are especially useful:
- Additional provider support (AWS Bedrock, Azure OpenAI, Mistral La Plateforme, Cohere, Groq)
- Admin REST API for key/project/limit management
- Prometheus metrics endpoint (`/metrics`)
- Additional embedding models for semantic cache
- Per-model-class routing rules (e.g. "always route coding tasks to X")

---

## License

MIT — see [LICENSE](LICENSE).
