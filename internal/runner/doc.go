// Package runner provides the core domain types and subsystems for
// github-runner-exporter.
//
// # Overview
//
// The package is composed of four concerns:
//
//  1. Configuration (config.go) — loads and validates the .runner JSON file
//     written by the GitHub Actions runner on registration, and provides
//     platform-appropriate default installation paths.
//
//  2. Log parsing (log_parser.go) — converts raw Runner_*.log lines into
//     typed Events and extracts WorkerMeta from Worker_*.log files. Supports
//     both the legacy flat-JSON format (runner < 2.333) and the structured
//     multi-line JSON format introduced in runner ≥ 2.333.
//
//  3. State tracking (job_tracker.go) — Tracker is a thread-safe state
//     machine that accepts Events and WorkerMeta values and maintains the
//     runner's current state (offline/idle/busy) together with per-job
//     metadata and Prometheus counter, histogram, and gauge instruments.
//
//  4. File watching (log_watcher.go) — Watcher monitors the _diag directory
//     using fsnotify with a polling fallback, drives the startup replay
//     sequence, and feeds live events and metadata to the Tracker.
//
// # Startup Sequence
//
// To avoid polluting permanent Prometheus series with "unknown" labels from
// partially-observed historical jobs, the Tracker begins in replay mode
// during startup: counters and histograms are suppressed while the active
// Runner log is replayed and existing Worker logs are walked for metadata.
// Once both passes complete the Tracker enters live mode and records all
// subsequent job completions normally.
//
// # Concurrency
//
// Tracker is the only type in this package designed for concurrent access.
// All exported methods on Tracker acquire the internal sync.RWMutex; callers
// need not synchronise externally. All other types are expected to be used
// from a single goroutine.
package runner
