# github-runner-exporter

Prometheus exporter for self-hosted GitHub Actions runners on long-lived VMs. Parses local `_diag/` log files directly — no GitHub API token required, no rate limits, works in air-gapped environments. Runs on Linux, macOS, and Windows alongside your existing runner process.

## Quick Start

```bash
# Linux / macOS
./github-runner-exporter --runner-dir ~/actions-runner

# Windows (PowerShell)
.\github-runner-exporter.exe --runner-dir C:\actions-runner

# Docker
docker run -d \
  -p 9102:9102 \
  -v /actions-runner:/runner:ro \
  ghcr.io/jnovack/github-runner-exporter:latest \
  --runner-dir /runner
```

Metrics are available at `http://localhost:9102/metrics`.

### Prometheus Scrape Config

```yaml
scrape_configs:
  - job_name: github-runner-exporter
    static_configs:
      - targets:
          - runner-prod-01.example.com:9102
          - runner-prod-02.example.com:9102
```

### Grafana Dashboards

Import the dashboards from [`deployments/grafana/`](deployments/grafana/):

| Dashboard | Purpose |
| --- | --- |
| `fleet-health.json` | Operational overview — online/busy/idle counts, VM health (requires `node_exporter`) |
| `job-analytics.json` | Performance analytics — duration histograms, success rates, top repos/workflows |

See [docs/grafana.md](docs/grafana.md) for import instructions and template variable setup.

## How It Works

The exporter watches the runner's `_diag/` directory:

- **`Runner_*.log`** — parsed for job start/complete events and runner online/offline state
- **`Worker_*.log`** — parsed for job metadata: `repo`, `workflow`, `job_name`, `actor` (runner v2.333.1+ multi-line format)

On startup it replays the most recent `Runner_*.log` to reconstruct current state. Log rotation (on runner restart) is handled automatically via `fsnotify`.

## Metrics

| Metric | Type | Description |
| --- | --- | --- |
| `github_runner_info` | Gauge | Static runner identity (name, group, OS) |
| `github_runner_online` | Gauge | 1 when listening for jobs |
| `github_runner_busy` | Gauge | 1 when a job is executing |
| `github_runner_jobs_total` | Counter | Completed jobs by `runner_name`, `repo`, `workflow`, `job_name`, `actor`, `status` |
| `github_runner_job_duration_seconds` | Histogram | Job durations by `runner_name`, `repo`, `workflow`, `job_name`, `actor` (buckets: 1s–1h) |
| `github_runner_current_job_info` | Gauge | Metadata for in-progress job (`runner_name`, `repo`, `workflow`, `job_name`, `actor`) |
| `github_runner_last_job_info` | Gauge | Metadata for most recently completed job (`runner_name`, `repo`, `workflow`, `job_name`, `actor`, `status`) |

Full metric reference: [docs/metrics.md](docs/metrics.md)

### Example Queries

```promql
# Average duration of the 'build' job over the last hour
rate(github_runner_job_duration_seconds_sum{job_name="build"}[1h])
  / rate(github_runner_job_duration_seconds_count{job_name="build"}[1h])

# How many times did CI run today?
sum(increase(github_runner_jobs_total{workflow="CI"}[24h]))

# p95 job duration on a specific runner
histogram_quantile(0.95,
  rate(github_runner_job_duration_seconds_bucket{runner_name="runner-prod-01"}[1h])
)

# Runner-seconds consumed per actor (chargeback / cost allocation)
sum by(actor) (increase(github_runner_job_duration_seconds_sum[1h]))

# Who is using the runners right now?
github_runner_busy == 1
  * on(runner_name) group_left(actor, repo, workflow)
  github_runner_current_job_info
```

## Configuration

| Flag | Env Var | Default | Description |
| --- | --- | --- | --- |
| `--runner-dir` | `RUNNER_DIR` | `/actions-runner` (Linux), `~/actions-runner` (macOS), `C:\actions-runner` (Windows) | Runner installation directory |
| `--listen-address` | `LISTEN_ADDRESS` | `:9102` | Metrics listen address |
| `--log-level` | `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `--version` | — | — | Print version, build date, revision and exit |

Full configuration reference: [docs/configuration.md](docs/configuration.md)

## Building

```bash
make build          # produces bin/github-runner-exporter
make test           # run tests
make test-race      # run tests with race detector
make docker         # build Docker image
```

## Deployment

See [docs/deployment.md](docs/deployment.md) for:

- systemd unit file (Linux)
- launchd plist (macOS)
- NSSM Windows service setup
- Docker with volume mount

## License

MIT
