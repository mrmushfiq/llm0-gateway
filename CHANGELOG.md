# Changelog

All notable changes to llm0-gateway are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased] — v0.1.1

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

These are loose ideas for v0.1.1 — promote to **Planned** when confirmed:

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

[Unreleased]: https://github.com/mrmushfiq/llm0-gateway/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/mrmushfiq/llm0-gateway/releases/tag/v0.1.0
