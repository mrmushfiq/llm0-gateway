-- LLM0 Gateway — Database Schema
-- PostgreSQL 15+  |  Requires pgvector extension for semantic caching
--
-- Run with:
--   psql $DATABASE_URL -f schema/schema.sql
-- Or via Docker Compose:
--   docker compose exec postgres psql -U llm0 -d llm0_gateway -f /schema/schema.sql

-- ============================================================================
-- EXTENSIONS
-- ============================================================================

CREATE EXTENSION IF NOT EXISTS "pgcrypto"; -- gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS vector;     -- pgvector (semantic caching)

-- ============================================================================
-- PROJECTS
-- Projects are the top-level resource. Each project has its own API keys,
-- cache settings, and spend cap. user_id is a free-form UUID you control —
-- no user table required for the self-hosted gateway.
-- ============================================================================

CREATE TABLE IF NOT EXISTS projects (
    id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,  -- Owner identifier; any UUID you manage
    name    VARCHAR(255) NOT NULL,

    -- Monthly spend cap (gateway blocks requests once exceeded)
    monthly_cap_usd         DECIMAL(10,2) DEFAULT 20.00,
    current_month_spend_usd DECIMAL(10,2) DEFAULT 0.00,
    spend_reset_at TIMESTAMPTZ DEFAULT date_trunc('month', NOW() + interval '1 month'),

    -- Cache settings (can also be set per-request via headers)
    cache_enabled          BOOLEAN     DEFAULT true,
    semantic_cache_enabled BOOLEAN     DEFAULT false,
    semantic_threshold     DECIMAL(3,2) DEFAULT 0.95, -- cosine similarity threshold
    cache_ttl_seconds      INT         DEFAULT 3600,  -- 1 hour

    is_active  BOOLEAN     DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_projects_user   ON projects(user_id);
CREATE INDEX IF NOT EXISTS idx_projects_active ON projects(is_active);

-- ============================================================================
-- API KEYS
-- Format: llm0_live_<32 hex chars>
-- The full key is shown once on creation; only the bcrypt hash is stored.
-- ============================================================================

CREATE TABLE IF NOT EXISTS api_keys (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,

    key_hash   VARCHAR(255) NOT NULL,  -- bcrypt hash of the raw key
    key_prefix VARCHAR(20)  NOT NULL,  -- first 15 chars + "..." shown in UI

    name               VARCHAR(255) NOT NULL,
    rate_limit_per_minute INT DEFAULT 60,

    is_active   BOOLEAN    DEFAULT true,
    last_used_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX        IF NOT EXISTS idx_api_keys_project ON api_keys(project_id, is_active);
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_hash    ON api_keys(key_hash);

-- ============================================================================
-- GATEWAY LOGS
-- One row per request. Tracks cost, latency, cache status, and failover info.
-- ============================================================================

CREATE TABLE IF NOT EXISTS gateway_logs (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID REFERENCES projects(id) ON DELETE CASCADE,
    api_key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,

    -- Request
    model    VARCHAR(100) NOT NULL,
    provider VARCHAR(50)  NOT NULL,

    -- Tokens & cost
    tokens_in    INT,
    tokens_out   INT,
    tokens_total INT,
    cost_usd     DECIMAL(10,6),

    -- Performance
    latency_ms         INT,
    cache_hit          BOOLEAN DEFAULT false,
    semantic_cache_hit BOOLEAN DEFAULT false,
    similarity_score   REAL,

    -- Routing & failover
    failover_count    INT DEFAULT 0,
    failover_occurred BOOLEAN DEFAULT false,
    final_provider    VARCHAR(50),

    -- Status
    status        VARCHAR(50),  -- 'success', 'error', 'rate_limited', 'cap_exceeded'
    error_message TEXT,

    -- Customer attribution (optional — set via X-Customer-ID header)
    customer_id VARCHAR(255),
    labels      JSONB,  -- Custom labels from X-LLM0-* headers

    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_gateway_logs_project_time ON gateway_logs(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_gateway_logs_cache        ON gateway_logs(project_id, cache_hit);
CREATE INDEX IF NOT EXISTS idx_gateway_logs_cost         ON gateway_logs(project_id, cost_usd);
CREATE INDEX IF NOT EXISTS idx_gateway_logs_customer     ON gateway_logs(customer_id, created_at DESC)
    WHERE customer_id IS NOT NULL;

-- ============================================================================
-- FAILOVER LOGS
-- Detailed record of every provider switch for debugging and analytics.
-- ============================================================================

CREATE TABLE IF NOT EXISTS failover_logs (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID REFERENCES projects(id) ON DELETE CASCADE,
    request_id VARCHAR(255),

    original_model    VARCHAR(100) NOT NULL,
    original_provider VARCHAR(50)  NOT NULL,
    fallback_model    VARCHAR(100) NOT NULL,
    fallback_provider VARCHAR(50)  NOT NULL,

    trigger_reason       VARCHAR(50) NOT NULL,  -- 'rate_limit', 'timeout', 'server_error'
    trigger_status_code  INT,
    trigger_error_message TEXT,

    original_attempt_latency_ms INT,
    fallback_latency_ms         INT,
    total_latency_ms            INT,

    fallback_succeeded     BOOLEAN NOT NULL,
    fallback_error_message TEXT,

    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_failover_logs_project  ON failover_logs(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_failover_logs_trigger  ON failover_logs(trigger_reason, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_failover_logs_provider ON failover_logs(original_provider, fallback_provider);

-- ============================================================================
-- CUSTOMER RATE LIMITING
-- Per-end-user spend caps and request limits within a project.
-- customer_id comes from the X-Customer-ID request header.
-- ============================================================================

CREATE TABLE IF NOT EXISTS customer_limits (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    customer_id VARCHAR(255) NOT NULL,

    -- Spend limits
    daily_spend_limit_usd   DECIMAL(10,2),
    monthly_spend_limit_usd DECIMAL(10,2),
    per_request_max_usd     DECIMAL(10,2),

    -- Request limits
    requests_per_minute INT,
    requests_per_hour   INT,
    requests_per_day    INT,

    -- Per-model limits (JSONB): {"gpt-4o": 50, "gpt-4o-mini": null}
    -- null = unlimited, number = max requests per day for that model
    model_limits JSONB,

    -- What to do when a limit is hit: 'block' | 'downgrade' | 'warn'
    on_limit_behavior VARCHAR(20) DEFAULT 'block',
    downgrade_model   VARCHAR(100),  -- used when on_limit_behavior = 'downgrade'

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(project_id, customer_id)
);

CREATE INDEX IF NOT EXISTS idx_customer_limits_project          ON customer_limits(project_id);
CREATE INDEX IF NOT EXISTS idx_customer_limits_project_customer ON customer_limits(project_id, customer_id);

-- Customer spend tracking (actual usage per day)
CREATE TABLE IF NOT EXISTS customer_spend (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    customer_id VARCHAR(255) NOT NULL,

    date DATE NOT NULL,
    hour INT,  -- 0-23 for hourly, NULL for daily aggregate

    total_spend_usd DECIMAL(10,6) DEFAULT 0,
    request_count   INT           DEFAULT 0,

    spend_by_model JSONB DEFAULT '{}'::jsonb,

    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(project_id, customer_id, date, hour)
);

CREATE INDEX IF NOT EXISTS idx_customer_spend_project_customer ON customer_spend(project_id, customer_id, date);
CREATE INDEX IF NOT EXISTS idx_customer_spend_date             ON customer_spend(date);

-- Helper: total spend for a customer on a given day
CREATE OR REPLACE FUNCTION get_customer_daily_spend(
    p_project_id  UUID,
    p_customer_id VARCHAR(255),
    p_date        DATE DEFAULT CURRENT_DATE
) RETURNS DECIMAL(10,6) LANGUAGE plpgsql AS $$
DECLARE v_total DECIMAL(10,6);
BEGIN
    SELECT COALESCE(SUM(total_spend_usd), 0)
    INTO v_total
    FROM customer_spend
    WHERE project_id  = p_project_id
      AND customer_id = p_customer_id
      AND date        = p_date;
    RETURN v_total;
END; $$;

-- Helper: total spend for a customer in a given month
CREATE OR REPLACE FUNCTION get_customer_monthly_spend(
    p_project_id  UUID,
    p_customer_id VARCHAR(255),
    p_year  INT DEFAULT EXTRACT(YEAR  FROM CURRENT_DATE)::INT,
    p_month INT DEFAULT EXTRACT(MONTH FROM CURRENT_DATE)::INT
) RETURNS DECIMAL(10,6) LANGUAGE plpgsql AS $$
DECLARE v_total DECIMAL(10,6);
BEGIN
    SELECT COALESCE(SUM(total_spend_usd), 0)
    INTO v_total
    FROM customer_spend
    WHERE project_id  = p_project_id
      AND customer_id = p_customer_id
      AND EXTRACT(YEAR  FROM date) = p_year
      AND EXTRACT(MONTH FROM date) = p_month;
    RETURN v_total;
END; $$;

-- ============================================================================
-- EXACT-MATCH CACHE
-- Two-tier: Redis (hot, sub-ms) + Postgres (warm, persistent).
-- Cache key = SHA-256 of (project_id + model + normalized messages).
-- ============================================================================

CREATE TABLE IF NOT EXISTS exact_cache (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID REFERENCES projects(id) ON DELETE CASCADE,

    cache_key VARCHAR(64) UNIQUE NOT NULL,  -- SHA-256 hex

    provider VARCHAR(50)  NOT NULL,
    model    VARCHAR(100) NOT NULL,

    prompt_tokens     INT,
    completion_tokens INT,

    cached_response JSONB NOT NULL,

    hit_count  INT         DEFAULT 0,
    last_hit_at TIMESTAMPTZ DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL,

    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_exact_cache_key          ON exact_cache(cache_key);
CREATE INDEX IF NOT EXISTS idx_exact_cache_project      ON exact_cache(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_exact_cache_expires      ON exact_cache(expires_at);
CREATE INDEX IF NOT EXISTS idx_exact_cache_provider_model ON exact_cache(provider, model);

-- ============================================================================
-- SEMANTIC CACHE
-- Vector similarity search via pgvector.
-- Requires the embedding service to be running (see EMBEDDING_SERVICE_URL).
-- Embeddings: all-MiniLM-L6-v2 (384 dimensions, self-hosted, free).
-- ============================================================================

CREATE TABLE IF NOT EXISTS semantic_cache (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID REFERENCES projects(id) ON DELETE CASCADE,

    cache_key VARCHAR(64) UNIQUE NOT NULL,

    provider VARCHAR(50)  NOT NULL,
    model    VARCHAR(100) NOT NULL,

    embedding       VECTOR(384) NOT NULL,  -- 384-dim all-MiniLM-L6-v2
    original_prompt TEXT        NOT NULL,

    cached_response   JSONB NOT NULL,
    prompt_tokens     INT,
    completion_tokens INT,

    hit_count   INT         DEFAULT 0,
    last_hit_at  TIMESTAMPTZ DEFAULT NOW(),
    expires_at   TIMESTAMPTZ NOT NULL,

    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- HNSW index for fast approximate nearest-neighbour search
CREATE INDEX IF NOT EXISTS idx_semantic_cache_embedding ON semantic_cache
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

CREATE INDEX IF NOT EXISTS idx_semantic_cache_project       ON semantic_cache(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_semantic_cache_expires       ON semantic_cache(expires_at);
CREATE INDEX IF NOT EXISTS idx_semantic_cache_provider_model ON semantic_cache(provider, model);

-- ============================================================================
-- MODEL PRICING
-- Used by the cost calculator to estimate request cost before calling the provider.
-- ============================================================================

CREATE TABLE IF NOT EXISTS model_pricing (
    id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider VARCHAR(50)  NOT NULL,
    model    VARCHAR(100) NOT NULL,

    input_per_1k_tokens  DECIMAL(10,8),
    output_per_1k_tokens DECIMAL(10,8),
    context_window       INT,
    supports_streaming   BOOLEAN DEFAULT true,
    supports_functions   BOOLEAN DEFAULT false,

    updated_at TIMESTAMPTZ DEFAULT NOW(),

    UNIQUE(provider, model)
);

CREATE INDEX IF NOT EXISTS idx_model_pricing_lookup ON model_pricing(provider, model);

-- NOTE: Default model pricing is seeded from schema/seed_models.sql.
-- Docker Compose mounts that file into the postgres initdb directory, so it
-- runs automatically on first boot. For non-Docker setups, after applying
-- this file run:  psql $DATABASE_URL -f schema/seed_models.sql
--
-- The seed uses ON CONFLICT DO NOTHING, so it's safe to re-run and will
-- never overwrite user-managed entries (e.g. from scripts/manage_models.sh).

-- ============================================================================
-- AUTO-UPDATE TRIGGER
-- ============================================================================

CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DO $$ BEGIN
    CREATE TRIGGER trg_projects_updated_at        BEFORE UPDATE ON projects        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
EXCEPTION WHEN duplicate_object THEN NULL; END; $$;

DO $$ BEGIN
    CREATE TRIGGER trg_api_keys_updated_at        BEFORE UPDATE ON api_keys        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
EXCEPTION WHEN duplicate_object THEN NULL; END; $$;

DO $$ BEGIN
    CREATE TRIGGER trg_customer_limits_updated_at BEFORE UPDATE ON customer_limits FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
EXCEPTION WHEN duplicate_object THEN NULL; END; $$;

DO $$ BEGIN
    CREATE TRIGGER trg_customer_spend_updated_at  BEFORE UPDATE ON customer_spend  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
EXCEPTION WHEN duplicate_object THEN NULL; END; $$;
