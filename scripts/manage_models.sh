#!/usr/bin/env bash
# =============================================================================
# manage_models.sh — interactive CRUD for the model_pricing table
#
# Usage:
#   ./scripts/manage_models.sh              # interactive menu
#   ./scripts/manage_models.sh list
#   ./scripts/manage_models.sh add
#   ./scripts/manage_models.sh update
#   ./scripts/manage_models.sh delete
#
# Requires: docker compose is running (postgres container)
# =============================================================================
set -euo pipefail

# ── Helpers ──────────────────────────────────────────────────────────────────
# NOTE: We close stdin (</dev/null) on every psql call so that `docker compose
# exec -T` does NOT swallow the script's read-prompt input when run interactively
# or via piped heredocs.
PSQL_BIN="docker compose exec -T postgres psql -U llm0 -d llm0_gateway"
psql_run() { $PSQL_BIN "$@" </dev/null; }
PSQL="psql_run"

color_title()   { printf "\033[1;36m%s\033[0m\n" "$1"; }
color_success() { printf "\033[1;32m%s\033[0m\n" "$1"; }
color_warn()    { printf "\033[1;33m%s\033[0m\n" "$1"; }
color_error()   { printf "\033[1;31m%s\033[0m\n" "$1"; }
color_dim()     { printf "\033[2m%s\033[0m\n" "$1"; }

divider() {
  echo "════════════════════════════════════════════════════════════════════════════════"
}

require_postgres() {
  if ! docker compose ps postgres 2>/dev/null | grep -q "Up\|running"; then
    color_error "❌ Postgres container is not running."
    echo "   Start it with: docker compose up -d postgres"
    exit 1
  fi
}

# Strip whitespace / psql exit-code noise from single-value queries.
strip() { tr -d '[:space:]'; }

# ── Commands ─────────────────────────────────────────────────────────────────

cmd_list() {
  divider
  color_title "  Model Pricing — All Entries"
  divider
  $PSQL -c "SELECT
              provider,
              model,
              input_per_1k_tokens  AS input_1k,
              output_per_1k_tokens AS output_1k,
              context_window       AS ctx,
              supports_streaming   AS stream,
              supports_functions   AS fn,
              updated_at::date     AS updated
            FROM model_pricing
            ORDER BY provider, model;"
}

cmd_add() {
  divider
  color_title "  Add New Model Pricing"
  divider

  local provider model input_price output_price context streaming functions

  echo ""
  echo "Provider options:"
  echo "  • openai"
  echo "  • anthropic"
  echo "  • google"
  echo "  • ollama       (local, usually priced at 0)"
  echo "  • <custom>"
  echo ""
  read -rp "Provider: " provider
  read -rp "Model ID (e.g. gpt-5-nano, claude-opus-5): " model

  # Check for duplicates up front.
  local exists
  exists=$($PSQL -tAc \
    "SELECT 1 FROM model_pricing WHERE provider='$provider' AND model='$model';" | strip)
  if [[ "$exists" == "1" ]]; then
    color_error "❌ Model '$provider/$model' already exists. Use 'update' instead."
    exit 1
  fi

  echo ""
  color_dim "Prices are per 1,000 tokens in USD. Example for gpt-4o-mini: 0.00015 input, 0.0006 output."
  read -rp "Input price per 1K tokens  [0.00000000]: "  input_price
  read -rp "Output price per 1K tokens [0.00000000]: " output_price
  read -rp "Context window (tokens)    [128000]:     "     context
  read -rp "Supports streaming? (y/n)  [y]:           " streaming
  read -rp "Supports functions? (y/n)  [n]:           " functions

  input_price=${input_price:-0.00000000}
  output_price=${output_price:-0.00000000}
  context=${context:-128000}
  streaming=${streaming:-y}
  functions=${functions:-n}

  local stream_bool=true fn_bool=false
  [[ "$streaming" =~ ^[Nn] ]] && stream_bool=false
  [[ "$functions" =~ ^[Yy] ]] && fn_bool=true

  $PSQL -c \
    "INSERT INTO model_pricing
       (provider, model, input_per_1k_tokens, output_per_1k_tokens,
        context_window, supports_streaming, supports_functions)
     VALUES
       ('$provider', '$model', $input_price, $output_price,
        $context, $stream_bool, $fn_bool);"

  echo ""
  color_success "✅ Added $provider / $model"
  color_warn "⚠️  Restart the gateway to reload pricing:  docker compose restart gateway"
  divider
}

cmd_update() {
  divider
  color_title "  Update Model Pricing"
  divider

  read -rp "Provider: " provider
  read -rp "Model ID: " model

  local exists
  exists=$($PSQL -tAc \
    "SELECT 1 FROM model_pricing WHERE provider='$provider' AND model='$model';" | strip)
  if [[ "$exists" != "1" ]]; then
    color_error "❌ Model '$provider/$model' not found."
    exit 1
  fi

  echo ""
  color_dim "Current values:"
  $PSQL -c \
    "SELECT input_per_1k_tokens AS input_1k,
            output_per_1k_tokens AS output_1k,
            context_window       AS ctx
     FROM model_pricing
     WHERE provider='$provider' AND model='$model';"
  echo ""
  color_dim "Press ENTER to keep the current value for any field."

  read -rp "New input price per 1K tokens:  " input_price
  read -rp "New output price per 1K tokens: " output_price
  read -rp "New context window:              " context

  local sets=()
  [[ -n "$input_price"  ]] && sets+=("input_per_1k_tokens = $input_price")
  [[ -n "$output_price" ]] && sets+=("output_per_1k_tokens = $output_price")
  [[ -n "$context"      ]] && sets+=("context_window = $context")

  if [[ ${#sets[@]} -eq 0 ]]; then
    color_warn "Nothing to update."
    exit 0
  fi

  sets+=("updated_at = NOW()")
  local set_clause
  set_clause=$(IFS=, ; echo "${sets[*]}")

  $PSQL -c \
    "UPDATE model_pricing SET $set_clause
     WHERE provider='$provider' AND model='$model';"

  echo ""
  color_success "✅ Updated $provider / $model"
  color_warn "⚠️  Restart the gateway to reload pricing:  docker compose restart gateway"
  divider
}

cmd_delete() {
  divider
  color_title "  Delete Model Pricing"
  divider

  read -rp "Provider: " provider
  read -rp "Model ID: " model

  local exists
  exists=$($PSQL -tAc \
    "SELECT 1 FROM model_pricing WHERE provider='$provider' AND model='$model';" | strip)
  if [[ "$exists" != "1" ]]; then
    color_error "❌ Model '$provider/$model' not found."
    exit 1
  fi

  read -rp "Are you sure? This cannot be undone. (y/N): " confirm
  [[ "$confirm" =~ ^[Yy]$ ]] || { color_warn "Cancelled."; exit 0; }

  $PSQL -c \
    "DELETE FROM model_pricing
     WHERE provider='$provider' AND model='$model';"

  echo ""
  color_success "✅ Deleted $provider / $model"
  color_warn "⚠️  Restart the gateway to reload pricing:  docker compose restart gateway"
  divider
}

cmd_menu() {
  divider
  color_title "  LLM0 Gateway — Model Pricing Manager"
  divider
  echo ""
  echo "  1) List all models"
  echo "  2) Add a new model"
  echo "  3) Update an existing model's pricing"
  echo "  4) Delete a model"
  echo "  5) Exit"
  echo ""
  read -rp "Choose an option [1-5]: " choice

  case "$choice" in
    1) cmd_list ;;
    2) cmd_add ;;
    3) cmd_update ;;
    4) cmd_delete ;;
    5) exit 0 ;;
    *) color_error "Invalid option"; exit 1 ;;
  esac
}

# ── Entry point ──────────────────────────────────────────────────────────────
require_postgres

case "${1:-menu}" in
  list)   cmd_list ;;
  add)    cmd_add ;;
  update) cmd_update ;;
  delete) cmd_delete ;;
  menu)   cmd_menu ;;
  -h|--help|help)
    echo "Usage: $0 [list|add|update|delete]"
    exit 0
    ;;
  *)
    color_error "Unknown command: $1"
    echo "Usage: $0 [list|add|update|delete]"
    exit 1
    ;;
esac
