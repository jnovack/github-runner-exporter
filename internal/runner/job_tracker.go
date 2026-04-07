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
	jobsTotal   *prometheus.CounterVec
	jobDuration *prometheus.HistogramVec
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
		jobDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "github_runner_job_duration_seconds",
				Help:    "Duration of completed jobs in seconds.",
				Buckets: []float64{1, 5, 30, 60, 120, 300, 600, 1800, 3600},
			},
			[]string{"runner_name", "repo", "workflow", "job_name", "actor"},
		),
	}

	reg.MustRegister(t.jobsTotal, t.jobDuration)
	return t
}

// HandleEvent updates tracker state in response to a parsed runner log event.
func (t *Tracker) HandleEvent(ev Event) {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch ev.Kind {
	case EventOnline:
		t.state = StateIdle

	case EventJobStarted:
		t.state = StateBusy
		t.current = &JobInfo{
			RunnerName: t.runnerName,
			JobName:    ev.JobName,
			StartedAt:  ev.Timestamp,
		}
		t.applyPendingMeta()

	case EventJobCompleted:
		if t.current == nil {
			// Received completion without a start — treat as best-effort.
			t.current = &JobInfo{
				RunnerName: t.runnerName,
				JobName:    ev.JobName,
				StartedAt:  ev.Timestamp,
			}
		}
		t.applyPendingMeta()

		t.current.Status = ev.Result
		t.current.EndedAt = ev.Timestamp
		if !t.current.StartedAt.IsZero() {
			t.current.Duration = t.current.EndedAt.Sub(t.current.StartedAt)
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
	t.jobDuration.WithLabelValues(job.RunnerName, repo, workflow, jobName, actor).Observe(job.Duration.Seconds())
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
