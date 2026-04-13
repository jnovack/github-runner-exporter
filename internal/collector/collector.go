package collector

import (
	"github.com/jnovack/github-runner-exporter/internal/runner"
	"github.com/prometheus/client_golang/prometheus"
)

// namespace is the common metric name prefix shared by all github_runner_*
// metrics produced by this exporter.
const namespace = "github_runner"

// ─── Collector ───────────────────────────────────────────────────────────────

// Collector implements prometheus.Collector for runner state gauges.
// Counter and histogram instruments live in the Tracker and are registered
// directly; this collector only handles gauges whose label values change
// per-scrape (current job info, last job info, online/busy state).
type Collector struct {
	tracker *runner.Tracker

	descInfo             *prometheus.Desc
	descOnline           *prometheus.Desc
	descBusy             *prometheus.Desc
	descCurrentJobInfo   *prometheus.Desc
	descCurrentJobStart  *prometheus.Desc
	descLastJobInfo      *prometheus.Desc
	descLastJobDuration  *prometheus.Desc
	descLastJobTimestamp *prometheus.Desc
}

// New creates a Collector and registers it with reg.
// version and revision are injected at build time via ldflags and exposed
// as constant labels on github_runner_info.
func New(tracker *runner.Tracker, cfg *runner.Config, version, revision string, reg prometheus.Registerer) *Collector {
	c := &Collector{
		tracker: tracker,

		descInfo: prometheus.NewDesc(
			namespace+"_info",
			"Static runner identity information.",
			nil,
			prometheus.Labels{
				"runner_name": cfg.AgentName,
				"group":       cfg.PoolName,
				"os":          runner.OS(),
				"version":     version,
				"revision":    revision,
			},
		),
		descOnline: prometheus.NewDesc(
			namespace+"_online",
			"1 if the runner is online and listening for jobs, 0 otherwise.",
			nil, nil,
		),
		descBusy: prometheus.NewDesc(
			namespace+"_busy",
			"1 if the runner is currently executing a job, 0 otherwise.",
			nil, nil,
		),
		descCurrentJobInfo: prometheus.NewDesc(
			namespace+"_current_job_info",
			"Metadata for the job currently being executed (present only when busy).",
			[]string{"runner_name", "repo", "workflow", "job_name", "actor"},
			nil,
		),
		descCurrentJobStart: prometheus.NewDesc(
			namespace+"_current_job_start_timestamp_seconds",
			"Unix timestamp when the current job started.",
			[]string{"runner_name"},
			nil,
		),
		descLastJobInfo: prometheus.NewDesc(
			namespace+"_last_job_info",
			"Metadata for the most recently completed job.",
			[]string{"runner_name", "repo", "workflow", "job_name", "actor", "status"},
			nil,
		),
		descLastJobDuration: prometheus.NewDesc(
			namespace+"_last_job_duration_seconds",
			"Duration in seconds of the most recently completed job.",
			[]string{"runner_name"},
			nil,
		),
		descLastJobTimestamp: prometheus.NewDesc(
			namespace+"_last_job_timestamp_seconds",
			"Unix timestamp when the most recently completed job finished.",
			[]string{"runner_name"},
			nil,
		),
	}

	reg.MustRegister(c)
	return c
}

// Describe sends all metric descriptors to ch.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.descInfo
	ch <- c.descOnline
	ch <- c.descBusy
	ch <- c.descCurrentJobInfo
	ch <- c.descCurrentJobStart
	ch <- c.descLastJobInfo
	ch <- c.descLastJobDuration
	ch <- c.descLastJobTimestamp
}

// Collect sends current metric values to ch based on a tracker snapshot.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	snap := c.tracker.Snapshot()

	ch <- prometheus.MustNewConstMetric(c.descInfo, prometheus.GaugeValue, 1)

	online, busy := stateToGauges(snap.State)
	ch <- prometheus.MustNewConstMetric(c.descOnline, prometheus.GaugeValue, online)
	ch <- prometheus.MustNewConstMetric(c.descBusy, prometheus.GaugeValue, busy)

	if snap.Current != nil {
		cur := snap.Current
		ch <- prometheus.MustNewConstMetric(
			c.descCurrentJobInfo, prometheus.GaugeValue, 1,
			cur.RunnerName,
			orUnknown(cur.Repo),
			orUnknown(cur.Workflow),
			orUnknown(cur.JobName),
			orUnknown(cur.Actor),
		)
		ch <- prometheus.MustNewConstMetric(
			c.descCurrentJobStart, prometheus.GaugeValue,
			float64(cur.StartedAt.Unix()),
			cur.RunnerName,
		)
	}

	if snap.Last != nil {
		last := snap.Last
		ch <- prometheus.MustNewConstMetric(
			c.descLastJobInfo, prometheus.GaugeValue, 1,
			last.RunnerName,
			orUnknown(last.Repo),
			orUnknown(last.Workflow),
			orUnknown(last.JobName),
			orUnknown(last.Actor),
			orUnknown(last.Status),
		)
		ch <- prometheus.MustNewConstMetric(
			c.descLastJobDuration, prometheus.GaugeValue,
			last.Duration.Seconds(),
			last.RunnerName,
		)
		ch <- prometheus.MustNewConstMetric(
			c.descLastJobTimestamp, prometheus.GaugeValue,
			float64(last.EndedAt.Unix()),
			last.RunnerName,
		)
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// stateToGauges converts a runner.State into (online, busy) gauge values.
func stateToGauges(s runner.State) (online, busy float64) {
	switch s {
	case runner.StateIdle:
		return 1, 0
	case runner.StateBusy:
		return 1, 1
	default: // StateOffline
		return 0, 0
	}
}

// orUnknown returns s if non-empty, or "unknown" as a Prometheus label
// fallback when job metadata has not yet been populated from a Worker log.
func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
