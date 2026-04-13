// Package collector implements the Prometheus scrape-time collector for
// github-runner-exporter.
//
// # Responsibilities
//
// The Collector type implements prometheus.Collector and is responsible for
// the subset of metrics whose label values can change between scrapes
// (gauges). It holds no mutable state of its own; on every Collect call it
// invokes runner.Tracker.Snapshot to obtain a consistent, point-in-time
// view of runner state, then translates that snapshot into metric samples.
//
// Counter and histogram instruments (total jobs, job durations) are
// registered directly by runner.Tracker when it is constructed and are not
// managed here.
//
// # Exported Metrics
//
//   - github_runner_info (gauge, constant labels)
//     Static identity information: runner name, group, OS, binary version, revision.
//
//   - github_runner_online (gauge)
//     1 when the runner is connected and listening for jobs, 0 otherwise.
//
//   - github_runner_busy (gauge)
//     1 while a job is being executed, 0 otherwise.
//
//   - github_runner_current_job_info (gauge)
//     Metadata labels for the job currently executing; absent when idle.
//
//   - github_runner_current_job_start_timestamp_seconds (gauge)
//     Unix timestamp of when the current job started.
//
//   - github_runner_last_job_info (gauge)
//     Metadata labels for the most recently completed job.
//
//   - github_runner_last_job_duration_seconds (gauge)
//     Elapsed seconds for the most recently completed job.
//
//   - github_runner_last_job_timestamp_seconds (gauge)
//     Unix timestamp of when the most recently completed job finished.
package collector
