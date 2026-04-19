-- =============================================================================
-- seed_models.sql — LLM0 Gateway canonical model pricing seed
--
-- This file is the SINGLE SOURCE OF TRUTH for default model pricing.
-- It is:
--   1. Mounted into the postgres container at first boot (see docker-compose.yml)
--   2. Embedded into the gateway binary (see internal/shared/database/seed.go)
--      and applied automatically the first time `model_pricing` is empty.
--
-- Safe to re-run: every INSERT uses ON CONFLICT (provider, model) DO NOTHING,
-- so existing rows (including user overrides from scripts/manage_models.sh)
-- are never touched.
--
-- When new models are released:
--   - Run scripts/manage_models.sh add     (for quick local overrides), OR
--   - Submit a PR updating this file       (so everyone benefits on upgrade)
--
-- Pricing is per 1,000 tokens in USD. Public pricing sources are cited inline.
-- Last reviewed: 2026-04-18
-- =============================================================================

INSERT INTO model_pricing
    (provider, model, input_per_1k_tokens, output_per_1k_tokens,
     context_window, supports_streaming, supports_functions)
VALUES
    -- ── OpenAI ────────────────────────────────────────────────────────────────
    -- https://openai.com/api/pricing
    ('openai', 'gpt-5.4',           0.00250,   0.01500,   1000000, true, true),  -- Apr 2026 flagship
    ('openai', 'gpt-5.4-mini',      0.00025,   0.00200,   1000000, true, true),  -- Apr 2026 — estimated
    ('openai', 'gpt-5.4-nano',      0.00010,   0.00080,   1000000, true, true),  -- Apr 2026 — estimated
    ('openai', 'gpt-4o',            0.00250,   0.01000,    128000, true, true),
    ('openai', 'gpt-4o-mini',       0.00015,   0.00060,    128000, true, true),
    ('openai', 'gpt-4-turbo',       0.01000,   0.03000,    128000, true, true),
    ('openai', 'gpt-3.5-turbo',     0.00050,   0.00150,     16385, true, true),

    -- ── Anthropic ─────────────────────────────────────────────────────────────
    -- https://docs.anthropic.com/en/docs/about-claude/pricing
    ('anthropic', 'claude-opus-4-7',             0.00500, 0.02500, 200000, true, false), -- Apr 2026 flagship
    ('anthropic', 'claude-opus-4-6',             0.01500, 0.07500, 200000, true, false),
    ('anthropic', 'claude-sonnet-4-6',           0.00300, 0.01500, 200000, true, false),
    ('anthropic', 'claude-opus-4-5-20251101',    0.01500, 0.07500, 200000, true, false),
    ('anthropic', 'claude-sonnet-4-5-20250929',  0.00300, 0.01500, 200000, true, false),
    ('anthropic', 'claude-haiku-4-5-20251001',   0.00080, 0.00400, 200000, true, false),
    ('anthropic', 'claude-sonnet-4-20250514',    0.00300, 0.01500, 200000, true, false),
    ('anthropic', 'claude-3-haiku-20240307',     0.00025, 0.00125, 200000, true, false),

    -- ── Google Gemini ─────────────────────────────────────────────────────────
    -- https://ai.google.dev/gemini-api/docs/pricing
    ('google', 'gemini-2.5-pro',         0.00125, 0.01000, 2097152, true, false),
    ('google', 'gemini-2.5-flash',       0.00010, 0.00040, 1048576, true, false),
    ('google', 'gemini-2.0-flash',       0.00010, 0.00040, 1048576, true, false),
    ('google', 'gemini-2.0-flash-lite',  0.000075,0.00030, 1048576, true, false)

ON CONFLICT (provider, model) DO NOTHING;
