package runner

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// State represents the current state of the runner.
type State int

const (
	StateOffline State = iota
	StateIdle
	StateBusy
)

var completionStatuses = []string{"succeeded", "failed", "canceled"}

func (s State) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateBusy:
		return "busy"
	default:
		return "offline"
	}
}

// JobInfo holds the metadata and timing for a single job execution.
type JobInfo struct {
	RunnerName string
	Repo       string
	Workflow   string
	JobName    string
	RunID      string
	Actor      string
	Status     string
	StartedAt  time.Time
	EndedAt    time.Time
	Duration   time.Duration
}

// Tracker maintains the runner's current state and Prometheus metrics.
// It is safe for concurrent use.
type Tracker struct {
	mu         sync.RWMutex
	runnerName string
	state      State
	current    *JobInfo // non-nil while a job is running
	last       *JobInfo // most recently completed job

	// pendingMeta holds worker metadata received before/during a job.
	// Merged into current when a job is started or completed.
	pendingMeta *WorkerMeta

	// liveMode gates counter/histogram recording. It is false during startup
	// log replay so that historical events do not populate metrics with
	// incomplete (unknown) labels. Set to true by EnterLiveMode after replay.
	liveMode bool

	// Prometheus instruments — registered once, observed per job.
	jobsTotal               *prometheus.CounterVec
	jobsByRunnerStatusTotal *prometheus.CounterVec
	jobDuration             *prometheus.HistogramVec
}

// NewTracker creates a Tracker and registers its Prometheus instruments with reg.
// The tracker starts in live mode; call EnterReplayMode before replaying logs.
func NewTracker(runnerName string, reg prometheus.Registerer) *Tracker {
	t := &Tracker{
		runnerName: runnerName,
		liveMode:   true,

		jobsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "github_runner_jobs_total",
				Help: "Total number of jobs processed by this runner.",
			},
			[]string{"runner_name", "repo", "workflow", "job_name", "actor", "status"},
		),
		jobsByRunnerStatusTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "github_runner_jobs_by_runner_status_total",
				Help: "Total completed jobs by runner and terminal status.",
			},
			[]string{"runner_name", "status"},
		),
		jobDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "github_runner_job_duration_seconds",
				Help:    "Duration of completed jobs in seconds.",
				Buckets: []float64{1, 5, 30, 60, 120, 300, 600, 1800, 3600},
			},
			[]string{"runner_name", "repo", "workflow", "job_name", "actor"},
		),
	}

	reg.MustRegister(t.jobsTotal, t.jobsByRunnerStatusTotal, t.jobDuration)
	// Pre-seed low-cardinality status series so range queries can observe
	// transitions without waiting for all statuses to occur.
	for _, status := range completionStatuses {
		t.jobsByRunnerStatusTotal.WithLabelValues(runnerName, status).Add(0)
	}
	return t
}

// HandleEvent updates tracker state in response to a parsed runner log event.
func (t *Tracker) HandleEvent(ev Event) {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch ev.Kind {
	case EventOnline:
		t.state = StateIdle
		// If completion was missed, "Listening for Jobs" is authoritative that
		// no job is currently executing.
		t.current = nil

	case EventJobStarted:
		t.state = StateBusy
		if t.current == nil {
			t.current = &JobInfo{
				RunnerName: t.runnerName,
				JobName:    ev.JobName,
				StartedAt:  ev.Timestamp,
			}
		} else {
			// Merge duplicate start signals (e.g. JobDispatcher fallback + Running job).
			if t.current.StartedAt.IsZero() || (!ev.Timestamp.IsZero() && ev.Timestamp.Before(t.current.StartedAt)) {
				t.current.StartedAt = ev.Timestamp
			}
			if ev.JobName != "" {
				t.current.JobName = ev.JobName
			}
		}
		t.applyPendingMeta()
		if t.liveMode {
			t.preseedCurrentStatusSeries()
		}

	case EventJobCompleted:
		if t.current == nil {
			// Received completion without a start — treat as best-effort.
			t.current = &JobInfo{
				RunnerName: t.runnerName,
				JobName:    ev.JobName,
			}
		}
		t.applyPendingMeta()

		t.current.Status = ev.Result
		if t.current.EndedAt.IsZero() || ev.Timestamp.After(t.current.EndedAt) {
			t.current.EndedAt = ev.Timestamp
		}
		if t.current.StartedAt.IsZero() {
			t.current.StartedAt = t.current.EndedAt
		}
		if !t.current.StartedAt.IsZero() && !t.current.EndedAt.IsZero() {
			t.current.Duration = t.current.EndedAt.Sub(t.current.StartedAt)
			if t.current.Duration < 0 {
				t.current.Duration = 0
			}
		}

		t.last = t.current
		t.current = nil
		t.state = StateIdle
		if t.liveMode {
			t.recordCompletion(t.last)
		}
	}
}

// SetWorkerMeta stores metadata from a Worker_*.log for the current or next job.
func (t *Tracker) SetWorkerMeta(meta WorkerMeta) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pendingMeta = &meta
	if t.current != nil {
		t.applyPendingMeta()
		if t.liveMode {
			t.preseedCurrentStatusSeries()
		}
	}
}

// EnterReplayMode disables counter/histogram recording. Call before replaying
// historical log events so that jobs with incomplete metadata do not pollute
// the metrics with permanent "unknown" label values.
func (t *Tracker) EnterReplayMode() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.liveMode = false
}

// EnterLiveMode re-enables counter/histogram recording. Call after startup
// replay and Worker log walk are complete.
func (t *Tracker) EnterLiveMode() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.liveMode = true
}

// EnrichLastFromPendingMeta applies pendingMeta to the last completed job if
// the job has no repo set. Called once during startup after replaying the Runner
// log and walking existing Worker logs. Clears pendingMeta so it is not
// incorrectly applied to the next new job.
func (t *Tracker) EnrichLastFromPendingMeta() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last == nil || t.pendingMeta == nil || t.last.Repo != "" {
		return
	}
	m := t.pendingMeta
	if m.Repo != "" {
		t.last.Repo = m.Repo
	}
	if m.Workflow != "" {
		t.last.Workflow = m.Workflow
	}
	if m.RunID != "" {
		t.last.RunID = m.RunID
	}
	if m.Actor != "" {
		t.last.Actor = m.Actor
	}
	if m.JobName != "" && t.last.JobName == "" {
		t.last.JobName = m.JobName
	}
	t.pendingMeta = nil
}

// applyPendingMeta merges pendingMeta into current if both are non-nil.
// Must be called with t.mu held.
func (t *Tracker) applyPendingMeta() {
	if t.pendingMeta == nil || t.current == nil {
		return
	}
	m := t.pendingMeta
	if m.Repo != "" {
		t.current.Repo = m.Repo
	}
	if m.Workflow != "" {
		t.current.Workflow = m.Workflow
	}
	if m.RunID != "" {
		t.current.RunID = m.RunID
	}
	if m.Actor != "" {
		t.current.Actor = m.Actor
	}
	if !m.StartedAt.IsZero() && (t.current.StartedAt.IsZero() || m.StartedAt.Before(t.current.StartedAt)) {
		t.current.StartedAt = m.StartedAt
	}
	if !m.EndedAt.IsZero() && (t.current.EndedAt.IsZero() || m.EndedAt.After(t.current.EndedAt)) {
		t.current.EndedAt = m.EndedAt
	}
	// JobName from meta overrides the name parsed from the Runner log
	// only when the Runner log produced an empty name.
	if m.JobName != "" && t.current.JobName == "" {
		t.current.JobName = m.JobName
	}
	t.pendingMeta = nil
}

// recordCompletion observes histogram and increments counter for a finished job.
// Must be called with t.mu held.
func (t *Tracker) recordCompletion(job *JobInfo) {
	repo := orUnknown(job.Repo)
	workflow := orUnknown(job.Workflow)
	jobName := orUnknown(job.JobName)
	actor := orUnknown(job.Actor)
	status := orUnknown(job.Status)

	t.jobsTotal.WithLabelValues(job.RunnerName, repo, workflow, jobName, actor, status).Inc()
	t.jobsByRunnerStatusTotal.WithLabelValues(job.RunnerName, status).Inc()
	t.jobDuration.WithLabelValues(job.RunnerName, repo, workflow, jobName, actor).Observe(job.Duration.Seconds())
}

// preseedCurrentStatusSeries initializes status-labeled counters at zero for the
// current job labelset so Prometheus can observe 0->1 transitions.
// Must be called with t.mu held.
func (t *Tracker) preseedCurrentStatusSeries() {
	if t.current == nil {
		return
	}
	repo := orUnknown(t.current.Repo)
	workflow := orUnknown(t.current.Workflow)
	jobName := orUnknown(t.current.JobName)
	actor := orUnknown(t.current.Actor)
	for _, status := range completionStatuses {
		t.jobsTotal.WithLabelValues(t.current.RunnerName, repo, workflow, jobName, actor, status).Add(0)
	}
}

// PreseedJobLabels initializes zero-value counter series for the given label combination
// across all terminal statuses. Called during startup to restore label cardinality from
// historical Worker logs so series survive across exporter restarts without gaps.
func (t *Tracker) PreseedJobLabels(repo, workflow, jobName, actor string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := orUnknown(repo)
	w := orUnknown(workflow)
	j := orUnknown(jobName)
	a := orUnknown(actor)
	for _, status := range completionStatuses {
		t.jobsTotal.WithLabelValues(t.runnerName, r, w, j, a, status).Add(0)
	}
}

// Snapshot returns a point-in-time copy of the tracker state for metric collection.
// It is safe to call from any goroutine.
type Snapshot struct {
	RunnerName string
	State      State
	Current    *JobInfo // nil if idle or offline
	Last       *JobInfo // nil if no job has completed yet
}

func (t *Tracker) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()

	snap := Snapshot{
		RunnerName: t.runnerName,
		State:      t.state,
	}
	if t.current != nil {
		c := *t.current
		snap.Current = &c
	}
	if t.last != nil {
		l := *t.last
		snap.Last = &l
	}
	return snap
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
