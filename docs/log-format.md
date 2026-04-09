# Log File Format Reference

This document describes the structure and relationship between the two log file types
produced by a GitHub Actions self-hosted runner. Developers working on the parser or
backfill logic should read this before touching a real log file — the sanitized fixtures
in `test/` cover every scenario described here.

---

## File Naming

Both file types live in the runner's `_diag/` directory.

| Pattern | Example | Notes |
| --- | --- | --- |
| `Runner_YYYYMMDD-HHMMSS-utc.log` | `Runner_20240315-080001-utc.log` | One file per runner **process** lifetime |
| `Worker_YYYYMMDD-HHMMSS-utc.log` | `Worker_20240315-080510-utc.log` | One file per **job** execution |

The filename timestamp for both files is the moment the process started (UTC). For a
Worker log this is within seconds of the corresponding `Running job:` line in the Runner log.

---

## Runner_*.log

**Fixture:** `test/Runner_typical.log`, `test/Runner_rotation.log`, `test/Runner_old_format.log`, `test/Runner_windows.log`

**Runner ≥ 2.333.1 (current):**

```text
[YYYY-MM-DD HH:MM:SSZ LEVEL Component] WRITE LINE: YYYY-MM-DD HH:MM:SSZ: <message>
```

Meaningful messages are routed through the `Terminal` component with a `WRITE LINE:` prefix
and an embedded console timestamp. The outer log timestamp and the embedded timestamp are
identical in practice.

**Runner < 2.333.1 (legacy):**

```text
[YYYY-MM-DD HH:MM:SSZ LEVEL Component] <message>
```

No `WRITE LINE:` prefix. The component name differs (`Runner.Listener`, `JobDispatcher`
instead of `Terminal`), but the message text is identical.

The parser handles both formats via `normalizeWriteLineMessage()` in `internal/runner/log_parser.go`.

### Actionable Events

Three message strings are meaningful — everything else is noise and is silently skipped:

| Message | Event | Data extracted |
| --- | --- | --- |
| `Listening for Jobs` | `EventOnline` | timestamp |
| `Running job: <name>` | `EventJobStarted` | timestamp, job_name |
| `Job <name> completed with result: <Result>` | `EventJobCompleted` | timestamp, job_name, result |

### Cancellation Spelling

GitHub Actions runners ≥ 2.333.1 emit `Canceled` (US spelling, one "l"). Older runners
may emit `Cancelled` (British, two "l's"). `ParseLine` normalizes both to `"canceled"`,
so the Prometheus label value is always `canceled` regardless of runner version.

### What the Runner Log Contains

- **Timing:** `started_at` (from `Running job:` timestamp), `ended_at` (from `completed` timestamp), and therefore `duration`
- **job_name:** The display name of the job
- **status:** `succeeded`, `failed`, or `canceled`

### What the Runner Log Does NOT Contain

`repo`, `workflow`, `actor`, `run_id` — none of these appear anywhere in the Runner log.

---

## Worker_*.log

**Fixtures:** `test/Worker_succeeded.log`, `test/Worker_failed.log`, `test/Worker_cancelled.log`, `test/Worker_rerun.log`, `test/Worker_old_format.log`, `test/Worker_cancelled_early.log`

**Runner ≥ 2.333.1 (current, multi-line):**

The job metadata block appears approximately 400–600 lines into the file, embedded in a
JSON structure logged by the `Worker` component:

```text
[TS INFO Worker] Job message:
 {
  "jobDisplayName": "<job_name>",
  "jobId": "<uuid>",
  "contextData": {
    "github": {
      "t": 2,
      "d": [
        { "k": "repository",  "v": "owner/repo" },
        { "k": "repository_owner", "v": "owner" },
        { "k": "run_id",      "v": "1234567890" },
        { "k": "run_attempt", "v": "1" },
        { "k": "actor",       "v": "username" },
        { "k": "workflow",    "v": "Workflow Display Name" },
        ...many other k/v pairs...
      ]
    }
  }
}
```

The `d` array contains many key/value pairs. Only five are extracted; all others are
ignored by the parser:

| Key | Field |
| --- | --- |
| `jobDisplayName` (top-level field, not in `d[]`) | `job_name` |
| `repository` | `repo` |
| `run_id` | `run_id` |
| `actor` | `actor` |
| `workflow` | `workflow` |

**Runner < 2.333.1 (legacy, single-line flat JSON):**

Metadata appears as a single JSON object on one log line, early in the file:

```text
[TS INFO Worker] {"repository":"owner/repo","workflow_name":"CI","run_id":"123","actor":"monalisa","job_name":"build"}
```

Note the field name difference: `workflow_name` in the old format vs `workflow` in the
`d[]` array key for the new format. The `WorkerMeta` struct uses `json:"workflow_name"`
to handle old-format deserialization.

### What the Worker Log Contains

- `repo` — the only source for this label
- `workflow` — the only source for this label
- `actor` — the only source for this label
- `run_id` — the only source for this label
- `job_name` (via `jobDisplayName`) — also available in the Runner log; used for correlation

### What the Worker Log Does NOT Authoritatively Contain

The line `[INFO JobRunner] Job result after all job steps finish: Succeeded` appears near
the end of every Worker log, but it is **not** used as the source of truth for job status.
The Runner log is authoritative for status because it is written by the runner process
(not the worker), which is more reliable.

---

## Why Both Files Are Required

A complete Prometheus label set requires data from both files:

| Label | Source | Notes |
| --- | --- | --- |
| `status` | Runner log ONLY | `succeeded`, `failed`, or `canceled` |
| `started_at` | Runner log | Timestamp of `Running job:` event |
| `ended_at` / `duration` | Runner log | Timestamp of `completed with result:` event |
| `job_name` | Runner log (primary) | Also in Worker as `jobDisplayName` for correlation |
| `repo` | Worker log ONLY | |
| `workflow` | Worker log ONLY | |
| `actor` | Worker log ONLY | |
| `run_id` | Worker log ONLY | |

---

## Correlation Algorithm

A self-hosted runner executes **one job at a time**, making correlation unambiguous.
Either condition below is sufficient to match a Worker log to a Runner log job entry:

1. **Timestamp overlap:** The Worker filename timestamp falls within the job's
   `[Running job: timestamp, completed timestamp]` window from the Runner log.
2. **Name match:** `jobDisplayName` in the Worker log equals the `job_name` parsed from
   the Runner log events.

---

## Job Outcome Scenarios

### Succeeded

**Runner log:**

```text
Running job: build
Job build completed with result: Succeeded
```

**Worker log:** Fully written. All metadata fields present. Ends with:

```text
[TS INFO JobRunner] Job result after all job steps finish: Succeeded
```

**Fixture:** `test/Runner_typical.log` (first job), `test/Worker_succeeded.log`

---

### Failed

**Runner log:**

```text
Running job: test
Job test completed with result: Failed
```

**Worker log:** Fully written — the worker process runs all steps to conclusion even when
a step fails. All metadata fields present. Ends with:

```text
[TS INFO JobRunner] Job result after all job steps finish: Failed
```

This was empirically verified: a failed Worker log from a production runner was 4,213 lines
with all metadata at lines 587–679 and the result line at line 4,182.

**Fixture:** `test/Runner_typical.log` (second job), `test/Worker_failed.log`

---

### Canceled — Normal

**Runner log:**

```text
Running job: deploy
Job deploy completed with result: Canceled
```

**Worker log:** Fully written — contrary to intuition, GitHub Actions sends a cancellation
signal to the job steps but allows the worker process to run to completion. All metadata
fields are present. The worker writes its final result line:

```text
[TS INFO JobRunner] Job result after all job steps finish: Canceled
```

This was empirically verified: a canceled Worker log from a production runner was 3,786
lines with all metadata at lines 427–518 and the result line at line 3,757.

**Fixture:** `test/Runner_typical.log` (third job), `test/Worker_cancelled.log`

---

### Canceled — Early (Truncated)

If the runner process is killed with `SIGKILL` (e.g., OOM kill, host shutdown) rather
than via the normal GitHub cancellation flow, the Worker process may be terminated before
it writes the `Job message:` section (~lines 400–600). In that case the Worker log will
be truncated and contain **no parseable metadata**.

`ParseWorkerLog` returns an empty `WorkerMeta` for truncated files. The caller must not
record a metric with empty labels — the job will appear as `unknown` across all dimensions.
The current implementation (`liveMode` gate + `harvestedLogs` guard) handles this correctly
by not recording metrics when metadata fields are absent.

**Fixture:** `test/Worker_cancelled_early.log` (5 lines, no metadata)

---

## Backfill Implications

The current implementation replays only the **newest** `Runner_*.log` on startup, and
suppresses counter/histogram recording during replay to avoid `unknown` labels. This means:

- Jobs from previous runner process lifetimes (older `Runner_*.log` files) are **not**
  included in counter/histogram values after a restart.
- After restart, Prometheus `increase()` and `rate()` over the counters start from 0.

A full historical backfill would require:

1. Walking **all** `Runner_*.log` files (not just the newest), oldest-first
2. For each `(Running job: X, completed with result: Y)` pair, finding the matching Worker log
3. Recording the counter/histogram only when the Worker log has all five metadata fields
4. Skipping any job pair where the Worker log is absent, truncated, or has empty fields

---

## Test Fixture Index

| File | Runner version | Scenario | Jobs / Metadata |
| --- | --- | --- | --- |
| `test/Runner_typical.log` | ≥ 2.333.1 | Full session | build (succeeded), test (failed), deploy (canceled) |
| `test/Runner_rotation.log` | ≥ 2.333.1 | Short session for rotation testing | lint (succeeded) |
| `test/Runner_windows.log` | ≥ 2.333.1 | CRLF line endings | Same events as typical |
| `test/Runner_old_format.log` | < 2.333.1 | No WRITE LINE prefix | package (succeeded) |
| `test/Worker_succeeded.log` | ≥ 2.333.1 | Normal completion | build, octocat/hello-world, CI, monalisa, run 9000000001 |
| `test/Worker_failed.log` | ≥ 2.333.1 | Step exit code 1 | test, octocat/hello-world, CI, monalisa, run 9000000002 |
| `test/Worker_cancelled.log` | ≥ 2.333.1 | GitHub cancellation (complete log) | deploy, octocat/hello-world, Release, monalisa, run 9000000003 |
| `test/Worker_rerun.log` | ≥ 2.333.1 | run_attempt = 2 | build-docker-image, octocat/hello-world, CI, monalisa, run 9000000004 |
| `test/Worker_old_format.log` | < 2.333.1 | Flat JSON metadata | package, octocat/hello-world, Legacy Build, monalisa, run 9000000005 |
| `test/Worker_cancelled_early.log` | ≥ 2.333.1 | Process killed before metadata written | 5 lines, no parseable fields |

---

## Known Issues

### Matrix jobs

Matrix jobs produce a `jobDisplayName` of the form `build / ubuntu-latest` where the
part after the slash is the matrix dimension value. The Runner log emits the same composite
name. `ParseLine` and `ParseWorkerLog` handle this correctly (confirmed by the existing
`"matrix job name with slash"` test case in `log_parser_test.go`), but no Worker log
fixture for a real matrix job currently exists. The `TODO` comment in `log_parser_test.go`
tracks this gap.
