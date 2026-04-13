// Command github-runner-exporter is a Prometheus exporter for GitHub Actions
// self-hosted runners. It tails the runner's diagnostic logs (_diag directory),
// parses runner state transitions and job metadata, and exposes them as
// Prometheus gauge, counter, and histogram metrics via an HTTP /metrics endpoint.
//
// # Architecture
//
// The binary wires together three subsystems:
//
//  1. runner.Watcher — watches the _diag directory with fsnotify (plus a
//     polling fallback) and drives state transitions by parsing Runner_*.log
//     lines. On startup it replays the active log to reconstruct current state
//     and walks existing Worker_*.log files to pre-seed label cardinality.
//
//  2. runner.Tracker — a thread-safe state machine that receives parsed events
//     from the Watcher and maintains the current and last-completed job, plus
//     Prometheus counters and histograms registered directly with the metrics
//     registry.
//
//  3. collector.Collector — a prometheus.Collector whose Collect method takes a
//     Snapshot from the Tracker on every scrape and emits gauge metrics for
//     online/busy state, current-job info, and last-job info.
//
// # Data Flow
//
//	fsnotify / poll
//	  → Watcher.tailRunnerLog  → ParseLine       → Tracker.HandleEvent
//	  → Watcher.readWorkerLog  → ParseWorkerLog  → Tracker.SetWorkerMeta
//	  Prometheus scrape        → Collector.Collect ← Tracker.Snapshot
//
// # HTTP Endpoints
//
//   - /metrics — Prometheus metrics in OpenMetrics format
//   - /healthz  — simple liveness probe; always returns 200 OK
//
// # Flags
//
// All flags may also be set via environment variables (upper-cased, dashes
// replaced with underscores, prefixed with the binary name).
//
//   - --runner-dir       path to the GitHub Actions runner install directory
//   - --listen-address   TCP address for the HTTP server (default :9102)
//   - --log-level        slog verbosity: debug, info, warn, error
//   - --walk-window      how far back to scan Worker logs on startup
//   - --version          print version information and exit
package main
