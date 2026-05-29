# Grafana / Prometheus Debug Context

## Prometheus — Manual Data Fetching

### Instant query

```sh
curl -sG "$PROM_URL/api/v1/query" \
  --data-urlencode 'query=up' | jq '.data.result[]'
```

### Range query

```sh
curl -sG "$PROM_URL/api/v1/query_range" \
  --data-urlencode 'query=rate(http_requests_total[5m])' \
  --data-urlencode 'start=2024-01-01T00:00:00Z' \
  --data-urlencode 'end=2024-01-01T01:00:00Z' \
  --data-urlencode 'step=60s' | jq '.data.result[]'
```

### Metadata

```sh
# All metric names
curl -s "$PROM_URL/api/v1/label/__name__/values" | jq '.data[]'

# Label values for a metric
curl -s "$PROM_URL/api/v1/label/job/values" | jq '.data[]'

# Series matching a selector
curl -sG "$PROM_URL/api/v1/series" \
  --data-urlencode 'match[]=http_requests_total{job="api"}' | jq '.data[]'

# Metric metadata (help/type)
curl -s "$PROM_URL/api/v1/metadata?metric=http_requests_total" | jq '.'
```

### Targets & rules

```sh
curl -s "$PROM_URL/api/v1/targets" | jq '.data.activeTargets[] | {job,instance,health,lastError}'
curl -s "$PROM_URL/api/v1/rules" | jq '.data.groups[].rules[]'
curl -s "$PROM_URL/api/v1/alerts" | jq '.data.alerts[]'
```

---

## Grafana — Manual Data Fetching

### Auth helpers

```sh
# Basic auth
GAUTH="-u ${GRAFANA_USER}:${GRAFANA_PASS}"
# Token auth
GAUTH="-H 'Authorization: Bearer $GRAFANA_TOKEN'"
```

### Dashboards

```sh
# Search dashboards
curl -s $GAUTH "$GRAFANA_URL/api/search?query=&type=dash-db" | jq '.[] | {uid,title,url}'

# Get dashboard JSON (panels, queries, variables)
curl -s $GAUTH "$GRAFANA_URL/api/dashboards/uid/<UID>" | jq '.dashboard'

# Extract all panel queries from a dashboard
curl -s $GAUTH "$GRAFANA_URL/api/dashboards/uid/<UID>" \
  | jq '.dashboard.panels[] | {id,title,targets}'
```

### Datasources

```sh
curl -s $GAUTH "$GRAFANA_URL/api/datasources" | jq '.[] | {id,name,type,url}'
curl -s $GAUTH "$GRAFANA_URL/api/datasources/name/Prometheus" | jq '.'
```

### Query a panel directly (Grafana proxy to datasource)

```sh
# DS_ID = datasource id from above
curl -s $GAUTH -X POST "$GRAFANA_URL/api/ds/query" \
  -H 'Content-Type: application/json' \
  -d '{
    "queries": [{
      "refId": "A",
      "datasourceId": <DS_ID>,
      "expr": "rate(http_requests_total[5m])",
      "range": true,
      "intervalMs": 15000,
      "maxDataPoints": 300
    }],
    "from": "now-1h",
    "to": "now"
  }' | jq '.'
```

---

## Common Failure Patterns & Checks

| Symptom | Where to look | Quick check |
| --- | --- | --- |
| Panel shows "No data" | Target down / wrong labels | `curl $PROM_URL/api/v1/targets` |
| Graph looks flat/wrong | Rate window too short vs scrape interval | Verify `rate(m[Xm])` — X ≥ 4× scrape interval |
| Stale data | Scrape failing silently | Check `up{job="..."}` and target `lastError` |
| Counter reset spike | Counter restarted | Use `increase()` or `resets()` to verify |
| Variable not populating | Bad label_values query | Test query directly in Explore |
| Inconsistent resolution | `step` too coarse | Reduce step or use `min_step` override |
| High cardinality / slow | Too many label combos | `topk(10, count by (__name__)({__name__=~".+"}))` |

---

## PromQL Quick Reference

```promql
# Rates (always use on counters)
rate(metric[5m])           # per-second avg over window
irate(metric[5m])          # last two samples (spiky, real-time)
increase(metric[5m])       # total increase over window

# Aggregations
sum by (label)(metric)
avg without (instance)(metric)
topk(5, metric)

# Alerting / thresholds
metric > 0.9
(metric - metric offset 1h) / (metric offset 1h)   # % change

# Histograms
histogram_quantile(0.99, sum by (le)(rate(req_duration_bucket[5m])))

# Joins
metric_a * on(instance) group_left(env) metric_b

# Subquery (avoid — expensive)
max_over_time(rate(metric[5m])[30m:1m])
```

---

## Scrape Config Verification

```sh
# Check what Prometheus scraped for a target in the last interval
curl -sG "$PROM_URL/api/v1/query" \
  --data-urlencode 'query=scrape_duration_seconds{job="myjob"}' | jq '.'

curl -sG "$PROM_URL/api/v1/query" \
  --data-urlencode 'query=scrape_samples_scraped{job="myjob"}' | jq '.'
```

---

## Debug Workflow

1. **Confirm data exists in Prometheus first** — Explore → instant query before touching Grafana.
2. **Check target health** — `up == 0` means scrape failing; check `lastError`.
3. **Validate query in isolation** — paste the panel's PromQL into Prometheus `/graph`.
4. **Check time range alignment** — Grafana's `$__rate_interval` vs hardcoded windows.
5. **Inspect dashboard JSON** — variables, datasource UIDs, repeated panels, overrides.
6. **Test via Grafana proxy** (`/api/ds/query`) — rules out browser/plugin issues.
7. **Check recording rules** — if query is slow, see if a rule should pre-aggregate.

---

## Variables

Set these before running commands:

```sh
export PROM_URL=http://localhost:9090
export GRAFANA_URL=http://localhost:3000
export GRAFANA_USER=admin
export GRAFANA_PASS=admin
# or
export GRAFANA_TOKEN=glsa_...
```

---

## Admin Operations — Purging Bad Series

Requires the Prometheus admin API (`--web.enable-admin-api`).

### Audit first

```sh
# Count unknown-label job series
curl -sG "$PROM_URL/api/v1/series" \
  --data-urlencode 'match[]=github_runner_jobs_total{repo="unknown"}' | jq '.data | length'

# Check for inflated duration observations (anything over 1 hour)
curl -sG "$PROM_URL/api/v1/query" \
  --data-urlencode 'query=github_runner_job_duration_seconds_sum > 3600' \
  | jq '.data.result[]'
```

### Delete "unknown" label series

Targets all `github_runner_*` metrics where any of the four enriched labels resolved
to `"unknown"` due to a missing or truncated Worker log.

```sh
for metric in \
    github_runner_jobs_total \
    github_runner_job_duration_seconds_bucket \
    github_runner_job_duration_seconds_sum \
    github_runner_job_duration_seconds_count; do
  for label in repo workflow job_name actor; do
    curl -sS -X POST "$PROM_URL/api/v1/admin/tsdb/delete_series" \
      --data-urlencode "match[]=${metric}{${label}=\"unknown\"}"
  done
done
```

### Delete inflated duration series

The histogram is cumulative — a single bad observation (e.g. 9-hour spike from the
stale Worker-meta bug) is baked into `_sum` and the `+Inf` bucket and cannot be
removed surgically. The entire series must be dropped and rebuilt from scratch.

**Restart the exporter first** so the next scrape starts with clean in-memory state,
then delete the TSDB series:

```sh
for series in \
    github_runner_job_duration_seconds_bucket \
    github_runner_job_duration_seconds_sum \
    github_runner_job_duration_seconds_count; do
  curl -sS -X POST "$PROM_URL/api/v1/admin/tsdb/delete_series" \
    --data-urlencode "match[]=${series}"
done
```

### Compact tombstones

Run after all deletes to reclaim TSDB space immediately:

```sh
curl -sS -X POST "$PROM_URL/api/v1/admin/tsdb/clean_tombstones"
```

### Verify

```sh
# Expect 0 until next scrape, then fresh series with no unknown labels
curl -sG "$PROM_URL/api/v1/series" \
  --data-urlencode 'match[]=github_runner_job_duration_seconds_sum' | jq '.data | length'
```

Wait one scrape interval (15–30 s). Fresh series should appear with no `unknown`
labels and no inflated `_sum`. The "Duration Analysis" panel in Grafana should no
longer show the 9-hour row.
