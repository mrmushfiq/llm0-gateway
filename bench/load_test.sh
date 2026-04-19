#!/usr/bin/env bash
# bench/load_test.sh — LLM0 Gateway load test using `hey`
#
# Prerequisites:
#   brew install hey        (macOS)
#   go install github.com/rakyll/hey@latest  (any platform)
#
# Usage:
#   export LLM0_API_KEY=llm0_live_<your key>
#   ./bench/load_test.sh
#
#   Optional overrides:
#   BASE_URL=http://localhost:8080 CONCURRENCY=50 REQUESTS=500 ./bench/load_test.sh

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
CONCURRENCY="${CONCURRENCY:-20}"
REQUESTS="${REQUESTS:-200}"
API_KEY="${LLM0_API_KEY:-}"

if [[ -z "$API_KEY" ]]; then
  echo "ERROR: set LLM0_API_KEY before running this script."
  echo "  export LLM0_API_KEY=llm0_live_<your key>"
  exit 1
fi

if ! command -v hey &>/dev/null; then
  echo "ERROR: 'hey' not found. Install it first:"
  echo "  brew install hey"
  echo "  # or: go install github.com/rakyll/hey@latest"
  exit 1
fi

PAYLOAD='{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Reply with exactly three words."}]}'

separator() { printf '\n%.0s─%.0s' {1..40}; echo; }

# ── 1. Warm cache ─────────────────────────────────────────────────────────────
echo "Sending one request to warm the cache..."
curl -s -o /dev/null -w "Warm-up status: %{http_code}\n" \
  -X POST "$BASE_URL/v1/chat/completions" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d "$PAYLOAD"

sleep 1

# ── 2. Cache-hit benchmark (hot path) ────────────────────────────────────────
separator
echo "SCENARIO 1 — Cache-hit (Redis hot path)"
echo "  Concurrency : $CONCURRENCY"
echo "  Requests    : $REQUESTS"
echo "  Payload     : $PAYLOAD"
separator
hey -n "$REQUESTS" -c "$CONCURRENCY" \
  -m POST \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d "$PAYLOAD" \
  "$BASE_URL/v1/chat/completions"

# ── 3. Cache-bypass benchmark (unique prompts) ────────────────────────────────
separator
echo "SCENARIO 2 — Cache-bypass (unique prompts, live provider calls)"
echo "  Concurrency : 5   (throttled to avoid provider rate limits)"
echo "  Requests    : 20"
separator
hey -n 20 -c 5 \
  -m POST \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Say a random word."}],"temperature":1.0}' \
  "$BASE_URL/v1/chat/completions"

separator
echo "Done. Compare p50/p99 between the two scenarios:"
echo "  Scenario 1 (cache hit)  → measures gateway overhead only"
echo "  Scenario 2 (cache miss) → measures full provider round-trip"
