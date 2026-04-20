# Changelog

All notable changes to llm0-gateway are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [0.1.1] — 2026-02-11

Maintenance release. Fixes three latent bugs in the background-workers
subsystem that were masked in v0.1.0 because the scheduler was never actually
started, plus documents how spend caps reset.

### Fixed

- **Background workers now actually run.** `workers.NewScheduler(...).StartAll(ctx)`
  was defined but never called from `cmd/gateway/main.go`, so the monthly spend
  reset, cache cleanup, log retention, and reconciliation jobs were all dormant.
  Enforcement was unaffected (all spend caps read from Redis, not Postgres), but
  `projects.current_month_spend_usd` never zeroed, `gateway_logs` grew unbounded,
  and expired cache rows accumulated. Wired into `main.go` with a cancellable
  root context that stops workers on `SIGINT` / `SIGTERM`.
- **`resetMonthlySpend` produced invalid JSON.** The metadata column used
  `ctx.Value("now")` (always `nil`) producing `"reset_date": "<nil>"`, which
  Postgres rejected as invalid JSONB. The error was swallowed by a blank
  identifier on the `ExecContext` call. Now emits RFC3339 timestamps.
- **`reconcileCustomerSpend` used a stale Redis key pattern.** The job scanned
  `customer_spend:*:*:{date}` but the actual spend Lua script writes to
  `spend:customer:{project_id}:{customer_id}:daily:{date}`, so the drift
  check found zero keys every hour. Fixed pattern and parse offsets.

### Added

- **`system_logs` table** — audit trail written by the scheduler on every job
  run (monthly spend reset, cache cleanup, log cleanup, reconciliation). Added
  to `schema/schema.sql` as idempotent `CREATE TABLE IF NOT EXISTS`, so fresh
  Docker Compose installs pick it up automatically. Existing deployments need
  the one-line migration in the **Upgrade notes** below.
- **`DISABLE_BACKGROUND_WORKERS` environment variable** (default `false`) —
  set to `true` in multi-replica deployments where only one replica should run
  maintenance jobs, or in tests where you don't want scheduled goroutines.

### Docs

- New **"How Spend Caps Reset"** section in `README.md` documenting:
  - Redis date-stamped keys and how they rotate without manual intervention
  - Postgres reporting layer and the five goroutine-based scheduled jobs
  - Redis persistence caveat (AOF / RDB) and how to rebuild counters from
    `gateway_logs` if you lose Redis data
  - Manual override commands (`manage_limits.sh`, direct `redis-cli DEL`)
- Refreshed the short **Background Workers** blurb in the feature list with
  explicit names and schedules for each job, plus a pointer to the new section.
- Added `DISABLE_BACKGROUND_WORKERS` to the environment variables reference.

### Upgrade notes

Existing v0.1.0 deployments need to create the `system_logs` table before
restarting onto v0.1.1, otherwise the scheduler will log `relation
"system_logs" does not exist` on every job run (jobs still complete — they
just can't write their audit row):

```sql
CREATE TABLE IF NOT EXISTS system_logs (
    id          UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type  VARCHAR(64)   NOT NULL,
    message     TEXT          NOT NULL,
    metadata    JSONB         DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ   DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_system_logs_event_time ON system_logs(event_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_system_logs_created    ON system_logs(created_at DESC);
```

Apply it in place:

```bash
docker compose exec -T postgres psql -U llm0 -d llm0_gateway <<'SQL'
CREATE TABLE IF NOT EXISTS system_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type VARCHAR(64) NOT NULL,
    message TEXT NOT NULL,
    metadata JSONB DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_system_logs_event_time ON system_logs(event_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_system_logs_created ON system_logs(created_at DESC);
SQL
```

Fresh `docker compose up` installs pick this up automatically from the
updated `schema/schema.sql`.

---

## [Unreleased] — v0.1.2

### Planned

- **Filter empty-delta chunks from Ollama streams.** Ollama's OpenAI-compatible
  adapter emits a long run of `{"delta":{"role":"assistant"}}` chunks with no
  `content` field at the start of every stream (observed ~150 chunks on
  `gemma3:e2b`). These are valid SSE but wasteful — they consume bandwidth,
  inflate log volume, and add visible lag in any UI that shows "thinking"
  indicators per chunk. Drop chunks where both `delta.content` and
  `delta.tool_calls` are empty, unless the chunk carries `finish_reason` or
  the first `role` assignment. Cloud providers don't exhibit this pattern so
  the filter only activates on the Ollama path.
  - Location: `internal/gateway/handlers/chat_stream.go`, inside the provider
    stream loop after `stream.Recv()` and before `SendSSEChunk`.
  - Acceptance: `curl -N ... "stream": true` against a gemma/llama Ollama model
    should produce chunks that all carry visible `content`, with the first
    `role` chunk and the terminal `finish_reason` chunk preserved.
  - Keep it behind a flag (`OLLAMA_FILTER_EMPTY_CHUNKS=true` default) so users
    who actually want the raw Ollama byte-for-byte stream can opt out.

### Candidates (not committed)

These are loose ideas — promote to **Planned** when confirmed:

- Prometheus `/metrics` endpoint (counters for provider/model/status, latency
  histograms, cache hit rate, failover count, cost total).
- Add `xai-*` (Grok) provider — prefix-based routing is already in place.
- Add `deepseek-*` provider via their OpenAI-compatible endpoint.
- `/v1/embeddings` proxy so users can use the bundled embedding service
  through the same auth/rate-limit/spend-cap plumbing.
- Publish pre-built Docker images to GHCR.
- Document streaming integration recipes (LangChain, LlamaIndex, Vercel AI SDK)
  in `docs/integrations/`.

---

## [0.1.0] — 2026-04-20

First public release.

### Added

- **Four providers** behind a single OpenAI-compatible endpoint: OpenAI,
  Anthropic, Gemini, and local Ollama. Routing is prefix-based on model name
  (`gpt-*`, `claude-*`, `gemini-*`, anything else → Ollama).
- **Automatic cross-provider failover** on 429 / 5xx / 404 / timeout / network
  error. Configurable via `FAILOVER_MODE` (`cloud_first`, `local_first`,
  `cloud_only`, `local_only`) with tier-based Ollama model mapping.
- **Streaming (SSE)** across all four providers, normalized to OpenAI-compatible
  chunks. Trailing metadata frame carries `cost_usd`, `usage`, `latency_ms`,
  and `provider` before `[DONE]`. Server `WriteTimeout` is disabled per-request
  on streaming endpoints so long generations (o1, Claude extended thinking,
  Ollama on CPU) aren't truncated.
- **Exact-match cache** — SHA-256 prompt hash, two-tier Redis (hot) + Postgres
  (warm), configurable TTL. Toggleable per API key.
- **Semantic cache** — pgvector cosine similarity against a bundled
  `all-MiniLM-L6-v2` embedding sidecar. Paraphrased queries hit at 0.954
  similarity in ~41 ms, `$0` cost.
- **Token-bucket rate limiting** per API key, atomic Redis via Lua.
- **Per-customer spend caps** (daily/monthly USD) with `block` or `downgrade`
  overflow behavior. Per-project hard `monthly_cap_usd`.
- **Cost tracking** — pre-request estimation for cap enforcement plus
  post-request reconciliation against actual token usage. Ollama is always `$0`.
- **Request logging** — every call logged to `gateway_logs` with provider,
  model, tokens, cost, latency, cache status, similarity, failover path,
  customer ID, and arbitrary `X-LLM0-*` labels as JSONB.
- **Model management CLI** (`scripts/manage_models.sh`) for CRUD on the
  `model_pricing` table without writing raw SQL.
- **Database seeding** via `schema/seed_models.sql` loaded through
  `docker-entrypoint-initdb.d/`.
- **GitHub Actions CI** — build, vet, test on every push.
- `GET /v1/models` endpoint returning all configured cloud + local models.

---

[Unreleased]: https://github.com/mrmushfiq/llm0-gateway/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/mrmushfiq/llm0-gateway/releases/tag/v0.1.1
[0.1.0]: https://github.com/mrmushfiq/llm0-gateway/releases/tag/v0.1.0
