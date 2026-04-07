# Configuration

## Flags and Environment Variables

`github-runner-exporter` uses [`jnovack/flag`](https://github.com/jnovack/flag), which reads configuration from (in order of precedence):

1. Command-line flags
2. Environment variables
3. Default values

Flag names map to environment variables by uppercasing and replacing `-` with `_`.

| Flag | Environment Variable | Default | Description |
| --- | --- | --- | --- |
| `--runner-dir` | `RUNNER_DIR` | `/actions-runner` (Linux), `~/actions-runner` (macOS), `C:\actions-runner` (Windows) | Path to the GitHub Actions runner installation directory. |
| `--listen-address` | `LISTEN_ADDRESS` | `:9102` | Address and port to expose Prometheus metrics on. |
| `--log-level` | `LOG_LEVEL` | `info` | Log verbosity. One of: `debug`, `info`, `warn`, `error`. |
| `--version` | — | — | Print version, build date, and revision then exit. |

## Runner Directory

The exporter needs read access to two things inside `--runner-dir`:

- `.runner` — JSON config file created during `./config.sh`. Contains runner name, group, and work folder.
- `_diag/Runner_*.log` — Runner diagnostic logs. Written continuously while the runner process is alive.
- `_diag/Worker_*.log` — Per-job worker logs. Created at the start of each job execution. The exporter parses these for job metadata: `repo`, `workflow`, `job_name`, and `actor` using the v2.333.1+ multi-line JSON format (`jobDisplayName` and `{"k":"key","v":"value"}` context pairs).

### Default Paths by OS

| OS | Default `--runner-dir` |
| --- | --- |
| Linux | `/actions-runner` |
| macOS | `~/actions-runner` (resolved via `os.UserHomeDir()`) |
| Windows | `C:\actions-runner` |

Override with `--runner-dir` or the `RUNNER_DIR` environment variable if your runner is installed elsewhere.

## Metrics Endpoint

Metrics are served at `http://<listen-address>/metrics` in OpenMetrics format.

A health check endpoint is available at `http://<listen-address>/healthz` — returns `200 ok` when the process is running.

## Version Information

Run with `--version` to print build metadata and exit:

```text
{"level":"INFO","msg":"github-runner-exporter","version":"v1.2.3","build_rfc3339":"2024-03-15T08:00:00Z","revision":"a1b2c3d..."}
```

Version and revision are also exposed as constant labels on the `github_runner_info` metric:

```promql
group by (version, revision) (github_runner_info)
```

## Prometheus Scrape Configuration

```yaml
scrape_configs:
  - job_name: github-runner-exporter
    static_configs:
      - targets:
          - runner-prod-01.example.com:9102
          - runner-prod-02.example.com:9102
    # Optional: use the runner hostname as the instance label
    relabel_configs:
      - source_labels: [__address__]
        target_label: instance
        regex: '([^:]+).*'
        replacement: '$1'
```

## Windows Notes

### Invoking from PowerShell

```powershell
.\github-runner-exporter.exe --runner-dir C:\actions-runner --listen-address :9102
```

### Environment Variables in PowerShell

```powershell
$env:RUNNER_DIR = "C:\actions-runner"
$env:LISTEN_ADDRESS = ":9102"
.\github-runner-exporter.exe
```

### File Permissions

The exporter opens runner log files read-only (`os.O_RDONLY`). The runner process holds the log files open for writing but allows concurrent read access, so no special permissions are required beyond normal user read access to the runner directory.

### Running as a Windows Service

See [deployment.md](deployment.md) for NSSM-based service setup.
