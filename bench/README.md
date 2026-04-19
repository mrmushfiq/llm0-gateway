# Benchmarks

Load tests for the LLM0 Gateway using [`hey`](https://github.com/rakyll/hey).

## Quick start

```bash
# Install hey
brew install hey          # macOS
go install github.com/rakyll/hey@latest   # any platform

# Run against your local stack
export LLM0_API_KEY=llm0_live_<your key>
./bench/load_test.sh
```

The script runs two scenarios back-to-back:

| Scenario | What it measures |
|---|---|
| **Cache-hit** | Pure gateway overhead — Redis lookup, auth, response serialisation |
| **Cache-miss** | Full round-trip including provider API call |

## Tuning

```bash
# Higher concurrency, more requests
BASE_URL=http://localhost:8080 CONCURRENCY=50 REQUESTS=1000 ./bench/load_test.sh

# Against a remote deployment
BASE_URL=https://your-gateway.example.com ./bench/load_test.sh
```

## Interpreting results

`hey` prints a latency histogram and percentiles. Key numbers to watch:

- **p50 (median)** on cache-hit → gateway processing overhead.
- **p99** on cache-hit → tail latency, relevant for SLAs.
- **p50 cache-miss − p50 cache-hit** → true provider round-trip time.

## What to include in a PR

If you run these benchmarks and want to contribute numbers to the docs:

1. Describe your environment (cloud/on-prem, instance type, region).
2. Include the raw `hey` output.
3. Note warm-up strategy (number of warm-up requests before measuring).
