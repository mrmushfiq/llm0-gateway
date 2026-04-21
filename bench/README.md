# Benchmarks

Load tests for the LLM0 Gateway using [`hey`](https://github.com/rakyll/hey).

For the full methodology, deployment-comparison table, and caveats, see
[`README.md` → Performance](../README.md#performance) in the repo root.
This document is the practitioner's quick reference for running the
benchmark yourself.

## Quick start

```bash
# Install hey
brew install hey          # macOS
go install github.com/rakyll/hey@latest   # any platform

# Run against your local stack
export LLM0_API_KEY=llm0_live_<your key>
./bench/load_test.sh
```

The script:

1. Sends one warm-up request to populate the cache
2. Runs the cache-hit scenario (concurrency 20, 200 requests)
3. Runs the cache-bypass scenario (concurrency 5, 20 requests — throttled
   to avoid provider rate limits)

| Scenario | What it measures |
|---|---|
| **Cache-hit** | Pure gateway overhead — Redis lookup, auth, response serialisation |
| **Cache-miss** | Full round-trip including upstream provider API call |

## Tuning

```bash
# Higher concurrency, more requests
BASE_URL=http://localhost:8080 CONCURRENCY=50 REQUESTS=1000 ./bench/load_test.sh

# Against a remote deployment
BASE_URL=https://your-gateway.example.com ./bench/load_test.sh
```

## Interpreting results

**Two sets of numbers come out of this benchmark — use the right one.**

### `hey`'s client-side summary (the printed output)

Good for throughput and a quick sanity check. Includes local network stack,
`hey`'s own goroutine scheduling, TCP connection reuse, and mixes 200s
with 429s into a single histogram. Systematically **0.5–5 ms larger** at
the tail than the server-side numbers on Linux, and much larger on
Docker-for-Mac.

### `gateway_logs.latency_ms` (the authoritative number)

This is the number to quote. Captures only the gateway's own handler
time — from request arrival at the Go handler to response write. Per
status code and cache-hit/miss split, so 429s don't pollute your 200
percentiles.

Run this immediately after the benchmark finishes:

```bash
docker compose exec -T postgres psql -U llm0 -d llm0_gateway -c "
SELECT status,
       cache_hit,
       count(*)                                                   AS n,
       percentile_disc(0.5)  WITHIN GROUP (ORDER BY latency_ms)   AS p50,
       percentile_disc(0.95) WITHIN GROUP (ORDER BY latency_ms)   AS p95,
       percentile_disc(0.99) WITHIN GROUP (ORDER BY latency_ms)   AS p99
FROM gateway_logs
WHERE created_at > now() - interval '15 minutes'
GROUP BY status, cache_hit;"
```

Expected output shape:

```
 status  | cache_hit | n  | p50 | p95  | p99
---------+-----------+----+-----+------+------
 success | f         |  6 | 826 | 1856 | 1856
 success | t         | 78 |   4 |   12 |   16
```

- `cache_hit = t` → gateway overhead only (this is what you publish)
- `cache_hit = f` → dominated by upstream provider latency (varies
  wildly day-to-day based on OpenAI/Anthropic load — don't publish
  with small n)

## Run it more than once

p50 and p95 are stable run-to-run. **p99 wiggles ±3–7 ms** because it's
dominated by Go GC pauses and Redis connection warm-up, not CPU. Two
runs on the same 4 vCPU droplet in our published table gave p99s of
**16 ms and 23 ms**.

Recommendation: run the benchmark 3 times, publish the range for p99
(e.g. *"p99 16–23 ms across 3 runs"*), not a single cherry-picked number.

## Host matters more than CPU count for p50

Our published numbers (see main README Performance section):

| Deployment | p50 gateway overhead |
|---|---:|
| DO 4 vCPU Linux droplet | 3–4 ms |
| DO 2 vCPU Linux droplet | 7 ms |
| MacBook Air M4, native Go + Docker Desktop | 11 ms |

The laptop is slower than the $48/mo Linux droplet because Docker
Desktop on macOS routes container traffic through a VM network bridge
(~1–2 ms per Redis round trip). **Benchmark on Linux for representative
production numbers.**

## What to include in a PR

If you run this and want to contribute numbers to the docs:

1. Environment: cloud / on-prem, instance type, region, Go version
2. The raw `hey` output for Scenario 1
3. The `gateway_logs` SQL query output
4. Run the benchmark **at least 3 times** and quote the p99 range, not a
   single number
5. Note whether the gateway was a fresh process or had been running
   (GC state matters for p99)
