#!/usr/bin/env bash
# =============================================================================
# create_api_key.sh — create a project and API key for the LLM0 Gateway
#
# Usage:
#   ./scripts/create_api_key.sh
#
# Requires: docker compose is running (postgres + embedding containers)
# =============================================================================
set -euo pipefail

# ── 1. Generate the raw key ───────────────────────────────────────────────────
RAW_KEY="llm0_live_$(openssl rand -hex 32)"
KEY_PREFIX="${RAW_KEY:0:15}..."

echo ""
echo "════════════════════════════════════════════════"
echo "  LLM0 Gateway — Create API Key"
echo "════════════════════════════════════════════════"
echo ""
echo "▶  Generated key (save this — shown only once):"
echo ""
echo "   $RAW_KEY"
echo ""

# ── 2 & 3. Hash key and insert — all inside Postgres (pgcrypto, zero deps) ────
# SHA-256 is computed with pgcrypto's digest(), then bcrypt with crypt()/gen_salt('bf').
# This matches the gateway validator: bcrypt( hex( sha256(raw_key) ) )
PROJECT_ID=$(docker compose exec -T postgres psql -U llm0 -d llm0_gateway -tAc \
  "WITH ins AS (
     INSERT INTO projects (user_id, name, monthly_cap_usd, cache_enabled, semantic_cache_enabled)
     VALUES (gen_random_uuid(), 'Default Project', 50.00, true, true)
     RETURNING id
   ) SELECT id FROM ins;" | tr -d '[:space:]')

BCRYPT_HASH=$(docker compose exec -T postgres psql -U llm0 -d llm0_gateway -tAc \
  "SELECT crypt(encode(digest('$RAW_KEY', 'sha256'), 'hex'), gen_salt('bf', 12));" | tr -d '[:space:]')

echo "▶  Bcrypt hash generated (via pgcrypto)"

docker compose exec -T postgres psql -U llm0 -d llm0_gateway -c \
  "INSERT INTO api_keys (project_id, key_hash, key_prefix, name, rate_limit_per_minute)
   VALUES ('$PROJECT_ID', '$BCRYPT_HASH', '$KEY_PREFIX', 'Default Key', 60);"

echo ""
echo "▶  Project ID : $PROJECT_ID"
echo "▶  Key prefix : $KEY_PREFIX"
echo ""
echo "════════════════════════════════════════════════"
echo "  Test it:"
echo ""
echo "  curl http://localhost:8080/v1/chat/completions \\"
echo "    -H \"Authorization: Bearer $RAW_KEY\" \\"
echo "    -H \"Content-Type: application/json\" \\"
echo "    -d '{\"model\":\"gpt-4o-mini\",\"messages\":[{\"role\":\"user\",\"content\":\"Say hello!\"}]}'"
echo ""
echo "════════════════════════════════════════════════"
