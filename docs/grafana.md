# Grafana Dashboards

Two dashboards are provided in [`deployments/grafana/`](../deployments/grafana/).

| File | Purpose | Refresh |
| --- | --- | --- |
| `fleet-health.json` | Operational — "is anything broken right now?" | 30s |
| `job-analytics.json` | Performance — "how are workflows trending?" | 5m |

## Prerequisites

- Prometheus datasource configured in Grafana (default UID: `prometheus`)
- `github-runner-exporter` scrape targets configured in Prometheus
- `node_exporter` running on the same VMs (required for CPU/memory/disk panels in fleet-health)

## Importing Dashboards

1. In Grafana, go to **Dashboards → Import**
2. Click **Upload JSON file**
3. Select `deployments/grafana/fleet-health.json` or `job-analytics.json`
4. Select your Prometheus datasource when prompted
5. Click **Import**

Repeat for the second dashboard.

## Template Variables

**Fleet Health** uses a `$runner` variable (filters by `instance`).

**Job Analytics** uses four cascading variables:

| Variable | Label | Filters |
| --- | --- | --- |
| `$runner` | Runner | All runners or a single instance |
| `$repo` | Repository | Repos seen on the selected runner |
| `$workflow` | Workflow | Workflows seen for the selected repo |
| `$actor` | Actor | Actors (GitHub usernames) seen for the selected runner |

This lets you drill from fleet-wide view → single runner → single repo/workflow → individual developer.

To link from Fleet Health to Job Analytics for a specific runner, use Grafana's **Data links** feature — the `$runner` variable will carry over.

## node_exporter Join

Fleet Health panels for CPU, memory, and disk pull from `node_exporter` metrics. For the join to work correctly, the `instance` label must match between your `github-runner-exporter` and `node_exporter` scrape targets for the same host.

Example scrape config that ensures consistent `instance` labels:

```yaml
scrape_configs:
  - job_name: github-runner-exporter
    static_configs:
      - targets: ['runner-prod-01.example.com:9102']
        labels:
          instance: runner-prod-01

  - job_name: node
    static_configs:
      - targets: ['runner-prod-01.example.com:9100']
        labels:
          instance: runner-prod-01
```

## Dashboard 1: Runner Fleet Health

Designed for on-call / ops use. Key panels:

- **Online / Busy / Idle / Offline** — stat boxes at a glance
- **Runner Status Table** — name, online, busy, last job time, last job status
- **Currently Running Jobs** — live view of in-progress jobs with elapsed time
- **Recent Failures** — last failed job per runner
- **CPU / Memory / Disk** — VM health from node_exporter, same view as runner state

## Dashboard 2: Job & Workflow Analytics

Designed for developers and platform engineers. Key panels:

- **Job Volume Over Time** — rate of jobs stacked by status (succeeded/failed/canceled)
- **Success Rate** — overall % over time
- **Top Repos / Workflows** — bar charts of job counts over the last 24h
- **p50 / p95 / p99 Duration by Workflow** — time series from histogram
- **Job Duration Heatmap** — visual distribution of job durations
- **Runner Utilization** — % of time each runner spends busy vs idle
- **Slowest Jobs Table** — p95 duration grouped by runner, workflow, and job name
- **Actor Analysis** — chargeback / cost allocation panels:
  - *Top Actors by Job Count* — who triggers the most jobs
  - *Runner-Seconds by Actor* — cumulative compute time consumed per developer
  - *Busy Over Time by Actor* — join of `github_runner_busy` with `github_runner_current_job_info` to show which actor is holding each runner at any point in time
