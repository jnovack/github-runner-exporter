# Metrics Reference

All metrics are prefixed with `github_runner_`.

## Runner State

| Metric | Type | Labels | Description |
| --- | --- | --- | --- |
| `github_runner_info` | Gauge | `runner_name`, `group`, `os`, `version`, `revision` | Always `1`. Static identity and build information for this runner instance. |
| `github_runner_online` | Gauge | — | `1` when the runner process is alive and listening for jobs, `0` when offline. |
| `github_runner_busy` | Gauge | — | `1` when a job is currently executing, `0` when idle or offline. |

## Job Counters

These instruments accumulate in-memory for the lifetime of the exporter process. All completed jobs are recorded regardless of Prometheus scrape interval. Counter resets on exporter restart are handled gracefully by `increase()` in PromQL.

> **Replay mode:** On startup, the exporter replays the most recent `Runner_*.log` to reconstruct current state. Jobs seen during replay are **not** counted — counters only record jobs that complete after the exporter starts. This prevents permanent `unknown` label values from polluting metrics when metadata (repo, workflow, actor) is not yet available.

| Metric | Type | Labels | Description |
| --- | --- | --- | --- |
| `github_runner_jobs_total` | Counter | `runner_name`, `repo`, `workflow`, `job_name`, `actor`, `status` | Total completed jobs. `status` is one of `succeeded`, `failed`, `cancelled`. |
| `github_runner_job_duration_seconds` | Histogram | `runner_name`, `repo`, `workflow`, `job_name`, `actor` | Job duration in seconds. Buckets: 1, 5, 30, 60, 120, 300, 600, 1800, 3600. |

## Current Job

Present only while a job is running. Removed from the metric output when the runner goes idle.

| Metric | Type | Labels | Description |
| --- | --- | --- | --- |
| `github_runner_current_job_info` | Gauge | `runner_name`, `repo`, `workflow`, `job_name`, `actor` | Always `1` while busy. Metadata for the in-progress job. |
| `github_runner_current_job_start_timestamp_seconds` | Gauge | `runner_name` | Unix timestamp when the current job started. |

## Last Completed Job

Reflects the most recently completed job only. Useful for alerting on failures and tracking recency.

| Metric | Type | Labels | Description |
| --- | --- | --- | --- |
| `github_runner_last_job_info` | Gauge | `runner_name`, `repo`, `workflow`, `job_name`, `actor`, `status` | Always `1`. Metadata for the most recently completed job. |
| `github_runner_last_job_duration_seconds` | Gauge | `runner_name` | Duration in seconds of the most recently completed job. |
| `github_runner_last_job_timestamp_seconds` | Gauge | `runner_name` | Unix timestamp when the most recently completed job finished. |

## Label Values

| Label | Source | Notes |
| --- | --- | --- |
| `runner_name` | `.runner` config `AgentName` field | Matches the name registered in GitHub. |
| `group` | `.runner` config `PoolName` field | Runner group (pool). Empty string if not set. |
| `version` | Build-time ldflag (`-X main.version`) | Semver tag e.g. `v1.2.3`. Falls back to `runtime/debug` VCS info, then `dev`. |
| `revision` | Build-time ldflag (`-X main.revision`) | Full git SHA. Falls back to `runtime/debug` VCS info, then `local`. |
| `os` | `runtime.GOOS` | Operating system of the runner VM. |
| `repo` | Worker log `repository` context value | Format: `owner/repo`. Falls back to `unknown` if not parseable. |
| `workflow` | Worker log `workflow` context value | Display name of the workflow. Falls back to `unknown`. |
| `job_name` | Worker log `jobDisplayName` field | Job name from the workflow YAML (includes matrix dimension). Falls back to `unknown`. |
| `actor` | Worker log `triggeredBy` context value | GitHub username that triggered the workflow. Falls back to `unknown`. |
| `status` | Runner log completion result | Lowercase: `succeeded`, `failed`, `cancelled`. |

## Example PromQL Queries

```promql
# Is any runner currently busy?
sum(github_runner_busy) > 0

# What version is running across the fleet?
group by (version, revision) (github_runner_info)

# How many jobs did repo org/myapp run in the last 24h?
sum(increase(github_runner_jobs_total{repo="org/myapp"}[24h]))

# How many times did the CI workflow run today?
sum(increase(github_runner_jobs_total{workflow="CI"}[24h]))

# Average duration of the 'build' job over the last hour?
rate(github_runner_job_duration_seconds_sum{job_name="build"}[1h])
  / rate(github_runner_job_duration_seconds_count{job_name="build"}[1h])

# p95 job duration on runner-prod-01?
histogram_quantile(0.95,
  rate(github_runner_job_duration_seconds_bucket{runner_name="runner-prod-01"}[1h])
)

# Success rate across all runners (last hour)?
sum(rate(github_runner_jobs_total{status="succeeded"}[1h]))
  / sum(rate(github_runner_jobs_total[1h]))

# Runners that have been offline for more than 5 minutes?
(time() - github_runner_last_job_timestamp_seconds) > 300
  and github_runner_online == 0

# Total runner-seconds consumed by each actor (last 1h) — chargeback / cost allocation
sum by(actor) (increase(github_runner_job_duration_seconds_sum[1h]))

# Who is using the runners right now? (join busy state with current job metadata)
github_runner_busy == 1
  * on(runner_name) group_left(actor, repo, workflow)
  github_runner_current_job_info

# Job count per actor over the last 24h
sum by(actor) (increase(github_runner_jobs_total[24h]))
```
