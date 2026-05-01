# ADR-001: Split github_runner_info into two metrics

- **Status:** Accepted
- **Date:** 2026-04-30

## Context

The original `github_runner_info` metric carried five constant labels:

```text
github_runner_info{runner_name="…", group="…", os="…", version="…", revision="…"}
```

Two of those labels — `version` and `revision` — described the **exporter
binary** (build-time ldflags). Of the remaining three, `runner_name` and
`group` described the runner agent identity (from the `.runner` config file);
`os` described the host OS (from `runtime.GOOS`). Mixing unrelated subjects on
a single metric created several problems:

- **Semantic confusion.** A user querying `version` expected the runner agent
  version (e.g. `2.334.0`), not the exporter's semver tag.
- **Dashboarding friction.** Joining runner-identity labels (`runner_name`,
  `group`) with build-provenance labels (`revision`, `build_date`) required
  carving up a single metric whose purpose was unclear.
- **Missing context.** Useful runner-identity fields (`arch`, `ephemeral`)
  and standard exporter build fields (`goversion`, `build_date`) were absent
  because they had no obvious home on the single combined metric.

## Options considered

### Option A — Keep one metric, rename labels

Rename `version` to `exporter_version` and add `runner_version` alongside it.

Rejected: a single metric still mixes two concerns. The labelset grows
unbounded as new fields are added to either subject.

### Option B — Split into two separate metrics (chosen)

Introduce `github_runner_exporter_info` for exporter build metadata and
repurpose `github_runner_info` exclusively for runner-agent identity.

Accepted: clear separation of concerns, aligns with the Prometheus ecosystem
convention (e.g. `node_exporter_build_info` / `node_uname_info`).

### Option C — Drop github_runner_info and put all labels on other metrics

Rejected: constant-label info metrics are a well-established Prometheus
pattern for cross-metric joins. Dropping them would break existing dashboards
and PromQL queries.

## Decision

Split the single metric into two:

### `github_runner_info`

Identity information about the **GitHub Actions runner agent**.

| Label | Source |
| --- | --- |
| `runner_name` | `.runner` config `AgentName` |
| `group` | `.runner` config `PoolName` |
| `os` | `runtime.GOOS` |
| `arch` | `runtime.GOARCH` |
| `ephemeral` | `.runner` config `IsEphemeral` |
| `version` | Runner agent version, detected at startup (see below) |

### `github_runner_exporter_info`

Build provenance of the **exporter binary**.

| Label | Source |
| --- | --- |
| `version` | `-X main.version` ldflag |
| `revision` | `-X main.revision` ldflag (git SHA) |
| `os` | `runtime.GOOS` |
| `build_date` | `-X main.buildRFC3339` ldflag |
| `goversion` | `runtime.Version()` |

### Runner agent version detection

The runner agent version is not stored in the `.runner` config file. The
GitHub REST API was rejected (requires credentials, external dependency).
`LoadConfig` tries four file-based strategies in order at startup:

1. **`bin` symlink** — `os.Readlink("bin")` + `filepath.Base` strips the
   path, leaving `bin.2.334.0`; `CutPrefix("bin.")` yields the version.
   Fast, but only works where the runner install creates a symlink.

2. **`bin/Runner.Listener.deps.json`** — the .NET runtime dependency manifest
   shipped inside every runner `bin/` directory. Contains a
   `"Runner.Listener/2.334.0"` key regardless of whether `bin` is a symlink
   or a plain directory. Most authoritative file-based source.

3. **Most recent `bin.VERSION` directory** — some update mechanisms leave
   `bin` as a plain directory and write versioned sibling directories
   (`bin.2.334.0/`). The most recently modified one is the active version.

4. **`_diag/Runner_*.log` startup line** — the HostContext component logs
   `Well known directory 'Bin': '/actions-runner/bin.2.334.0'` on every
   start. Last resort; requires at least one completed startup log.

All four strategies produce `version` as a constant label before the first
scrape. Falls back to `"unknown"` only if none succeed.

## Consequences

- **Breaking change:** the `version` and `revision` labels are removed from
  `github_runner_info`. Dashboards or alerts that join on those labels must be
  updated to use `github_runner_exporter_info` instead.
- **New metric:** `github_runner_exporter_info` is added; Prometheus will begin
  scraping it on the first scrape after deploying the updated exporter.
- **New labels on `github_runner_info`:** `arch` and `ephemeral` are added.
  Existing queries that match all labels by exact equality will need updating;
  queries that select a subset of labels are unaffected.
- **Runner agent version accuracy:** the symlink approach works where the
  runner creates a `bin` symlink (standard Linux and macOS installs). Where the
  symlink is absent, `LoadConfig` falls back to scanning `_diag/Runner_*.log`
  for the HostContext startup line. Windows runners and non-standard layouts
  without either will still see `version="unknown"`.
# ADR-001: Split github_runner_info into two metrics

- **Status:** Accepted
- **Date:** 2026-04-30

## Context

The original `github_runner_info` metric carried five constant labels:

```text
github_runner_info{runner_name="…", group="…", os="…", version="…", revision="…"}
```

Two of those labels — `version` and `revision` — described the **exporter
binary** (build-time ldflags). Of the remaining three, `runner_name` and
`group` described the runner agent identity (from the `.runner` config file);
`os` described the host OS (from `runtime.GOOS`). Mixing unrelated subjects on
a single metric created several problems:

- **Semantic confusion.** A user querying `version` expected the runner agent
  version (e.g. `2.334.0`), not the exporter's semver tag.
- **Dashboarding friction.** Joining runner-identity labels (`runner_name`,
  `group`) with build-provenance labels (`revision`, `build_date`) required
  carving up a single metric whose purpose was unclear.
- **Missing context.** Useful runner-identity fields (`arch`, `ephemeral`)
  and standard exporter build fields (`goversion`, `build_date`) were absent
  because they had no obvious home on the single combined metric.

## Options considered

### Option A — Keep one metric, rename labels

Rename `version` to `exporter_version` and add `runner_version` alongside it.

Rejected: a single metric still mixes two concerns. The labelset grows
unbounded as new fields are added to either subject.

### Option B — Split into two separate metrics (chosen)

Introduce `github_runner_exporter_info` for exporter build metadata and
repurpose `github_runner_info` exclusively for runner-agent identity.

Accepted: clear separation of concerns, aligns with the Prometheus ecosystem
convention (e.g. `node_exporter_build_info` / `node_uname_info`).

### Option C — Drop github_runner_info and put all labels on other metrics

Rejected: constant-label info metrics are a well-established Prometheus
pattern for cross-metric joins. Dropping them would break existing dashboards
and PromQL queries.

## Decision

Split the single metric into two:

### `github_runner_info`

Identity information about the **GitHub Actions runner agent**.

| Label | Source |
| --- | --- |
| `runner_name` | `.runner` config `AgentName` |
| `group` | `.runner` config `PoolName` |
| `os` | `runtime.GOOS` |
| `arch` | `runtime.GOARCH` |
| `ephemeral` | `.runner` config `IsEphemeral` |
| `version` | Runner agent version, detected from the `bin` symlink (see below) |

### `github_runner_exporter_info`

Build provenance of the **exporter binary**.

| Label | Source |
| --- | --- |
| `version` | `-X main.version` ldflag |
| `revision` | `-X main.revision` ldflag (git SHA) |
| `os` | `runtime.GOOS` |
| `build_date` | `-X main.buildRFC3339` ldflag |
| `goversion` | `runtime.Version()` |

### Runner agent version detection

The runner agent version is not stored in the `.runner` config file. Three
approaches were evaluated:

1. **Parse the `_diag/Runner_*.log` startup line** — the HostContext component
   logs `Well known directory 'Bin': '/actions-runner/bin.2.334.0'` on every
   startup. Viable, but requires log replay to complete before the version is
   known, which would either delay metric availability or require a variable
   label that changes from `"unknown"` to the real value during the first
   scrape window.

2. **GitHub REST API** — the runner's `AgentId` and pool ID could be used to
   query the API. Rejected: requires authentication credentials and introduces
   an external network dependency.

3. **`bin` symlink in the runner directory** — every runner installation
   creates a `bin` symlink pointing to `bin.VERSION` (e.g. `bin.2.334.0`).
   Reading the symlink target with `os.Readlink` yields the version string
   immediately at startup, before the collector is constructed. Falls back to
   `"unknown"` if the symlink is absent or unreadable.

Option 3 is the primary strategy. If the symlink is absent, `LoadConfig`
falls back to Option 1: scanning the most recently created `Runner_*.log` in
`_diag/` for the HostContext `"Well known directory 'Bin'"` startup line.
Both strategies make `version` available as a constant label at startup.

## Consequences

- **Breaking change:** the `version` and `revision` labels are removed from
  `github_runner_info`. Dashboards or alerts that join on those labels must be
  updated to use `github_runner_exporter_info` instead.
- **New metric:** `github_runner_exporter_info` is added; Prometheus will begin
  scraping it on the first scrape after deploying the updated exporter.
- **New labels on `github_runner_info`:** `arch` and `ephemeral` are added.
  Existing queries that match all labels by exact equality will need updating;
  queries that select a subset of labels are unaffected.
- **Runner agent version accuracy:** the symlink approach works where the
  runner creates a `bin` symlink (standard Linux and macOS installs). Where the
  symlink is absent, `LoadConfig` falls back to scanning `_diag/Runner_*.log`
  for the HostContext startup line. Windows runners and non-standard layouts
  without either will still see `version="unknown"`.
