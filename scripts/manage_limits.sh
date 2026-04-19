#!/usr/bin/env bash
# =============================================================================
# manage_limits.sh — interactive CLI to tune rate limits, spend caps, and
# per-customer quotas WITHOUT writing SQL by hand.
#
# Covers three tables:
#   • api_keys.rate_limit_per_minute      (per-API-key req/min throttle)
#   • projects.monthly_cap_usd            (hard project-level spend ceiling)
#   • projects.cache_* / semantic_*       (cache toggles + TTL + threshold)
#   • customer_limits.*                   (per-end-user daily/monthly caps)
#
# Usage:
#   ./scripts/manage_limits.sh                     # interactive menu
#   ./scripts/manage_limits.sh list-keys
#   ./scripts/manage_limits.sh set-key-rate
#   ./scripts/manage_limits.sh list-projects
#   ./scripts/manage_limits.sh set-project-cap
#   ./scripts/manage_limits.sh set-project-cache
#   ./scripts/manage_limits.sh list-customers
#   ./scripts/manage_limits.sh set-customer-limit
#   ./scripts/manage_limits.sh delete-customer-limit
#
# Requires: docker compose is running (postgres container).
# =============================================================================
set -euo pipefail

# ── Helpers ──────────────────────────────────────────────────────────────────
# NOTE: We close stdin (</dev/null) on every psql call so that `docker compose
# exec -T` does NOT swallow the script's read-prompt input.
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

# Accept either a null/empty string or a non-negative integer.
read_int_or_null() {
  local prompt="$1"
  local var
  read -rp "$prompt" var
  if [[ -z "$var" ]]; then
    echo "NULL"
  elif [[ "$var" =~ ^[0-9]+$ ]]; then
    echo "$var"
  else
    color_error "Invalid integer: $var"
    exit 1
  fi
}

read_money_or_null() {
  local prompt="$1"
  local var
  read -rp "$prompt" var
  if [[ -z "$var" ]]; then
    echo "NULL"
  elif [[ "$var" =~ ^[0-9]+(\.[0-9]+)?$ ]]; then
    echo "$var"
  else
    color_error "Invalid amount: $var"
    exit 1
  fi
}

# ── API Keys ─────────────────────────────────────────────────────────────────

cmd_list_keys() {
  divider
  color_title "  API Keys"
  divider
  $PSQL -c "SELECT
              k.key_prefix           AS prefix,
              k.name                 AS name,
              p.name                 AS project,
              k.rate_limit_per_minute AS rate_per_min,
              k.is_active            AS active,
              k.last_used_at         AS last_used
            FROM api_keys k
            JOIN projects p ON p.id = k.project_id
            ORDER BY p.name, k.name;"
}

cmd_set_key_rate() {
  divider
  color_title "  Update API Key Rate Limit"
  divider
  cmd_list_keys
  echo ""
  local prefix new_rate
  read -rp "API key prefix to update (exact match, e.g. 'llm0_live_a7ea9...'): " prefix
  if [[ -z "$prefix" ]]; then
    color_error "Prefix required."
    exit 1
  fi
  read -rp "New rate_limit_per_minute (integer): " new_rate
  if ! [[ "$new_rate" =~ ^[0-9]+$ ]]; then
    color_error "Rate must be a non-negative integer."
    exit 1
  fi

  local updated
  updated=$($PSQL -t -A -c "UPDATE api_keys
                            SET rate_limit_per_minute = ${new_rate},
                                updated_at = NOW()
                            WHERE key_prefix = '${prefix}'
                            RETURNING id;" | tr -d '[:space:]')

  if [[ -z "$updated" ]]; then
    color_error "❌ No API key found with prefix '${prefix}'."
    exit 1
  fi
  color_success "✅ Rate limit for ${prefix} set to ${new_rate} req/min."
  color_dim "   Takes effect immediately — no restart required."
}

cmd_toggle_key() {
  divider
  color_title "  Enable / Disable API Key"
  divider
  cmd_list_keys
  echo ""
  local prefix active_str
  read -rp "API key prefix: " prefix
  read -rp "Set active? (y/n): " active_str
  local active="true"
  [[ "$active_str" =~ ^[Nn]$ ]] && active="false"

  local updated
  updated=$($PSQL -t -A -c "UPDATE api_keys
                            SET is_active = ${active}, updated_at = NOW()
                            WHERE key_prefix = '${prefix}'
                            RETURNING id;" | tr -d '[:space:]')
  if [[ -z "$updated" ]]; then
    color_error "❌ No API key found with prefix '${prefix}'."
    exit 1
  fi
  color_success "✅ ${prefix} is_active = ${active}"
}

# ── Projects ─────────────────────────────────────────────────────────────────

cmd_list_projects() {
  divider
  color_title "  Projects"
  divider
  $PSQL -c "SELECT
              id,
              name,
              monthly_cap_usd         AS cap_usd,
              current_month_spend_usd AS spent_usd,
              cache_enabled           AS cache,
              semantic_cache_enabled  AS sem_cache,
              semantic_threshold      AS sem_thresh,
              cache_ttl_seconds       AS cache_ttl,
              is_active               AS active
            FROM projects
            ORDER BY name;"
}

cmd_set_project_cap() {
  divider
  color_title "  Update Project Monthly Spend Cap"
  divider
  cmd_list_projects
  echo ""
  local project_id new_cap
  read -rp "Project ID: " project_id
  read -rp "New monthly_cap_usd (e.g. 100.00): " new_cap
  if ! [[ "$new_cap" =~ ^[0-9]+(\.[0-9]+)?$ ]]; then
    color_error "Cap must be a decimal (e.g. 100 or 100.00)."
    exit 1
  fi
  local updated
  updated=$($PSQL -t -A -c "UPDATE projects
                            SET monthly_cap_usd = ${new_cap}, updated_at = NOW()
                            WHERE id = '${project_id}'
                            RETURNING id;" | tr -d '[:space:]')
  if [[ -z "$updated" ]]; then
    color_error "❌ No project with id '${project_id}'."
    exit 1
  fi
  color_success "✅ Project ${project_id} monthly cap set to \$${new_cap}."
}

cmd_set_project_cache() {
  divider
  color_title "  Update Project Cache Settings"
  divider
  cmd_list_projects
  echo ""
  local project_id cache_enabled sem_cache_enabled threshold ttl

  read -rp "Project ID: " project_id
  read -rp "cache_enabled (y/n, blank = leave unchanged): " cache_enabled
  read -rp "semantic_cache_enabled (y/n, blank = leave unchanged): " sem_cache_enabled
  read -rp "semantic_threshold (0.0–1.0, blank = leave unchanged): " threshold
  read -rp "cache_ttl_seconds (integer, blank = leave unchanged): " ttl

  local sets=()
  [[ "$cache_enabled" =~ ^[Yy]$ ]]       && sets+=("cache_enabled = true")
  [[ "$cache_enabled" =~ ^[Nn]$ ]]       && sets+=("cache_enabled = false")
  [[ "$sem_cache_enabled" =~ ^[Yy]$ ]]   && sets+=("semantic_cache_enabled = true")
  [[ "$sem_cache_enabled" =~ ^[Nn]$ ]]   && sets+=("semantic_cache_enabled = false")
  [[ -n "$threshold" ]]                  && sets+=("semantic_threshold = ${threshold}")
  [[ -n "$ttl" ]]                        && sets+=("cache_ttl_seconds = ${ttl}")

  if [[ ${#sets[@]} -eq 0 ]]; then
    color_warn "No changes requested — skipping."
    return
  fi

  local set_clause
  set_clause=$(IFS=', '; echo "${sets[*]}")

  local updated
  updated=$($PSQL -t -A -c "UPDATE projects
                            SET ${set_clause}, updated_at = NOW()
                            WHERE id = '${project_id}'
                            RETURNING id;" | tr -d '[:space:]')
  if [[ -z "$updated" ]]; then
    color_error "❌ No project with id '${project_id}'."
    exit 1
  fi
  color_success "✅ Updated: ${set_clause}"
}

# ── Customer Limits ──────────────────────────────────────────────────────────

cmd_list_customers() {
  divider
  color_title "  Customer Limits"
  divider
  $PSQL -c "SELECT
              cl.customer_id          AS customer,
              p.name                  AS project,
              cl.daily_spend_limit_usd   AS day_usd,
              cl.monthly_spend_limit_usd AS month_usd,
              cl.requests_per_minute  AS rpm,
              cl.requests_per_hour    AS rph,
              cl.requests_per_day     AS rpd,
              cl.on_limit_behavior    AS on_limit,
              cl.downgrade_model      AS downgrade_to
            FROM customer_limits cl
            JOIN projects p ON p.id = cl.project_id
            ORDER BY p.name, cl.customer_id;"
}

cmd_set_customer_limit() {
  divider
  color_title "  Upsert Customer Limit"
  divider
  cmd_list_projects
  echo ""
  local project_id customer_id
  read -rp "Project ID: " project_id
  read -rp "Customer ID (free-form, e.g. 'user_42'): " customer_id
  [[ -z "$project_id" || -z "$customer_id" ]] && { color_error "Both required."; exit 1; }

  echo ""
  color_dim "Leave any field blank to store NULL (no limit on that axis)."
  echo ""
  local daily monthly rpm rph rpd behavior downgrade per_req_max

  daily=$(read_money_or_null        "daily_spend_limit_usd      : ")
  monthly=$(read_money_or_null      "monthly_spend_limit_usd    : ")
  per_req_max=$(read_money_or_null  "per_request_max_usd        : ")
  rpm=$(read_int_or_null            "requests_per_minute        : ")
  rph=$(read_int_or_null            "requests_per_hour          : ")
  rpd=$(read_int_or_null            "requests_per_day           : ")

  echo ""
  echo "on_limit_behavior options: block | downgrade | warn"
  read -rp "on_limit_behavior (default: block): " behavior
  behavior="${behavior:-block}"

  downgrade="NULL"
  if [[ "$behavior" == "downgrade" ]]; then
    read -rp "downgrade_model (e.g. gpt-4o-mini): " downgrade_val
    [[ -z "$downgrade_val" ]] && { color_error "downgrade_model required when behavior=downgrade."; exit 1; }
    downgrade="'${downgrade_val}'"
  fi

  $PSQL -c "INSERT INTO customer_limits
              (project_id, customer_id,
               daily_spend_limit_usd, monthly_spend_limit_usd, per_request_max_usd,
               requests_per_minute, requests_per_hour, requests_per_day,
               on_limit_behavior, downgrade_model)
            VALUES
              ('${project_id}', '${customer_id}',
               ${daily}, ${monthly}, ${per_req_max},
               ${rpm}, ${rph}, ${rpd},
               '${behavior}', ${downgrade})
            ON CONFLICT (project_id, customer_id) DO UPDATE SET
              daily_spend_limit_usd    = EXCLUDED.daily_spend_limit_usd,
              monthly_spend_limit_usd  = EXCLUDED.monthly_spend_limit_usd,
              per_request_max_usd      = EXCLUDED.per_request_max_usd,
              requests_per_minute      = EXCLUDED.requests_per_minute,
              requests_per_hour        = EXCLUDED.requests_per_hour,
              requests_per_day         = EXCLUDED.requests_per_day,
              on_limit_behavior        = EXCLUDED.on_limit_behavior,
              downgrade_model          = EXCLUDED.downgrade_model,
              updated_at               = NOW();"

  color_success "✅ Limit upserted for customer '${customer_id}' in project ${project_id}."
  color_dim "   The in-memory limit cache will refresh automatically within ~60s."
}

cmd_delete_customer_limit() {
  divider
  color_title "  Delete Customer Limit"
  divider
  cmd_list_customers
  echo ""
  local project_id customer_id confirm
  read -rp "Project ID: " project_id
  read -rp "Customer ID: " customer_id
  read -rp "Type DELETE to confirm: " confirm
  [[ "$confirm" == "DELETE" ]] || { color_warn "Aborted."; exit 0; }

  $PSQL -c "DELETE FROM customer_limits
            WHERE project_id = '${project_id}' AND customer_id = '${customer_id}';"
  color_success "✅ Customer limit removed."
}

# ── Menu ─────────────────────────────────────────────────────────────────────

show_menu() {
  divider
  color_title "  LLM0 Gateway — manage_limits.sh"
  divider
  echo ""
  echo "API Keys"
  echo "  1) List API keys"
  echo "  2) Update an API key's rate_limit_per_minute"
  echo "  3) Enable / disable an API key"
  echo ""
  echo "Projects"
  echo "  4) List projects"
  echo "  5) Update project monthly_cap_usd"
  echo "  6) Update project cache settings (exact + semantic)"
  echo ""
  echo "Customer Limits"
  echo "  7) List customer limits"
  echo "  8) Add / update a customer limit"
  echo "  9) Delete a customer limit"
  echo ""
  echo "  q) Quit"
  echo ""
  read -rp "Choose: " choice

  case "$choice" in
    1) cmd_list_keys ;;
    2) cmd_set_key_rate ;;
    3) cmd_toggle_key ;;
    4) cmd_list_projects ;;
    5) cmd_set_project_cap ;;
    6) cmd_set_project_cache ;;
    7) cmd_list_customers ;;
    8) cmd_set_customer_limit ;;
    9) cmd_delete_customer_limit ;;
    q|Q) exit 0 ;;
    *) color_error "Unknown choice: $choice"; exit 1 ;;
  esac
}

# ── Main ─────────────────────────────────────────────────────────────────────

require_postgres

if [[ $# -eq 0 ]]; then
  show_menu
  exit 0
fi

case "$1" in
  list-keys)               cmd_list_keys ;;
  set-key-rate)            cmd_set_key_rate ;;
  toggle-key)              cmd_toggle_key ;;
  list-projects)           cmd_list_projects ;;
  set-project-cap)         cmd_set_project_cap ;;
  set-project-cache)       cmd_set_project_cache ;;
  list-customers)          cmd_list_customers ;;
  set-customer-limit)      cmd_set_customer_limit ;;
  delete-customer-limit)   cmd_delete_customer_limit ;;
  *)
    color_error "Unknown command: $1"
    echo "Run './scripts/manage_limits.sh' with no args for the interactive menu."
    exit 1
    ;;
esac
