package runner

import (
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestStateString(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateOffline, "offline"},
		{StateIdle, "idle"},
		{StateBusy, "busy"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func newTestTracker(t *testing.T) *Tracker {
	t.Helper()
	reg := prometheus.NewRegistry()
	return NewTracker("test-runner", reg)
}

func ts(s string) time.Time {
	t, _ := time.Parse(logTimeLayout, s)
	return t.UTC()
}

// TestTracker_InitialState verifies a new tracker starts offline.
func TestTracker_InitialState(t *testing.T) {
	tr := newTestTracker(t)
	snap := tr.Snapshot()
	if snap.State != StateOffline {
		t.Errorf("initial state = %v, want StateOffline", snap.State)
	}
	if snap.Current != nil {
		t.Errorf("initial current = %v, want nil", snap.Current)
	}
	if snap.Last != nil {
		t.Errorf("initial last = %v, want nil", snap.Last)
	}
}

// TestTracker_OnlineTransition verifies OFFLINE → IDLE on EventOnline.
func TestTracker_OnlineTransition(t *testing.T) {
	tr := newTestTracker(t)
	tr.HandleEvent(Event{Kind: EventOnline, Timestamp: ts("2024-03-15 08:00:02")})
	snap := tr.Snapshot()
	if snap.State != StateIdle {
		t.Errorf("state after EventOnline = %v, want StateIdle", snap.State)
	}
}

// TestTracker_HappyPath exercises IDLE → BUSY → IDLE for a successful job.
func TestTracker_HappyPath(t *testing.T) {
	reg := prometheus.NewRegistry()
	tr := NewTracker("runner-q", reg)

	tr.HandleEvent(Event{Kind: EventOnline, Timestamp: ts("2024-03-15 08:00:02")})
	tr.SetWorkerMeta(WorkerMeta{
		Repo: "org/app", Workflow: "CI", RunID: "99", Actor: "alice", JobName: "build",
	})
	tr.HandleEvent(Event{Kind: EventJobStarted, Timestamp: ts("2024-03-15 08:05:10"), JobName: "build"})

	snap := tr.Snapshot()
	if snap.State != StateBusy {
		t.Fatalf("state after job started = %v, want StateBusy", snap.State)
	}
	if snap.Current == nil {
		t.Fatal("current job is nil while busy")
	}
	if snap.Current.Repo != "org/app" {
		t.Errorf("current.Repo = %q, want %q", snap.Current.Repo, "org/app")
	}

	tr.HandleEvent(Event{Kind: EventJobCompleted, Timestamp: ts("2024-03-15 08:07:45"), JobName: "build", Result: "succeeded"})

	snap = tr.Snapshot()
	if snap.State != StateIdle {
		t.Errorf("state after job completed = %v, want StateIdle", snap.State)
	}
	if snap.Current != nil {
		t.Errorf("current after completion = %v, want nil", snap.Current)
	}
	if snap.Last == nil {
		t.Fatal("last job is nil after completion")
	}
	if snap.Last.Status != "succeeded" {
		t.Errorf("last.Status = %q, want %q", snap.Last.Status, "succeeded")
	}
	if snap.Last.Duration == 0 {
		t.Error("last.Duration is zero")
	}

	// Counter should be 1.
	count := testutil.ToFloat64(tr.jobsTotal.WithLabelValues("runner-q", "org/app", "CI", "build", "alice", "succeeded"))
	if count != 1 {
		t.Errorf("jobsTotal counter = %v, want 1", count)
	}
}

// TestTracker_TwoJobsAccumulate verifies counter increments correctly across multiple jobs.
func TestTracker_TwoJobsAccumulate(t *testing.T) {
	reg := prometheus.NewRegistry()
	tr := NewTracker("runner-q", reg)

	for i := 0; i < 3; i++ {
		tr.HandleEvent(Event{Kind: EventOnline, Timestamp: ts("2024-03-15 08:00:00")})
		tr.SetWorkerMeta(WorkerMeta{Repo: "org/app", Workflow: "CI", RunID: "1", Actor: "alice", JobName: "build"})
		tr.HandleEvent(Event{Kind: EventJobStarted, Timestamp: ts("2024-03-15 08:05:00"), JobName: "build"})
		tr.HandleEvent(Event{Kind: EventJobCompleted, Timestamp: ts("2024-03-15 08:06:00"), JobName: "build", Result: "succeeded"})
	}

	count := testutil.ToFloat64(tr.jobsTotal.WithLabelValues("runner-q", "org/app", "CI", "build", "alice", "succeeded"))
	if count != 3 {
		t.Errorf("jobsTotal = %v after 3 jobs, want 3", count)
	}
}

// TestTracker_LastJobIsLatest verifies last reflects only the most recent job.
func TestTracker_LastJobIsLatest(t *testing.T) {
	tr := newTestTracker(t)

	for _, result := range []string{"succeeded", "failed", "succeeded"} {
		tr.HandleEvent(Event{Kind: EventOnline})
		tr.HandleEvent(Event{Kind: EventJobStarted, JobName: "build"})
		tr.HandleEvent(Event{Kind: EventJobCompleted, JobName: "build", Result: result})
	}

	snap := tr.Snapshot()
	if snap.Last == nil {
		t.Fatal("last is nil")
	}
	if snap.Last.Status != "succeeded" {
		t.Errorf("last.Status = %q, want \"succeeded\" (third job)", snap.Last.Status)
	}
}

// TestTracker_CompletionWithoutStart verifies graceful handling of orphaned completion events.
func TestTracker_CompletionWithoutStart(t *testing.T) {
	tr := newTestTracker(t)
	// Should not panic.
	tr.HandleEvent(Event{Kind: EventJobCompleted, JobName: "build", Result: "succeeded", Timestamp: ts("2024-03-15 08:07:45")})
	snap := tr.Snapshot()
	if snap.Last == nil {
		t.Error("last should be set even for orphaned completion")
	}
}

// TestTracker_ReplayModeSkipsCounters verifies that jobs processed during replay
// do not populate counters or histograms (metadata is not yet available during replay).
func TestTracker_ReplayModeSkipsCounters(t *testing.T) {
	reg := prometheus.NewRegistry()
	tr := NewTracker("runner-q", reg)
	tr.EnterReplayMode()

	tr.HandleEvent(Event{Kind: EventOnline})
	tr.HandleEvent(Event{Kind: EventJobStarted, JobName: "build"})
	tr.HandleEvent(Event{Kind: EventJobCompleted, JobName: "build", Result: "succeeded"})

	// Counter should be zero — recorded during replay.
	count := testutil.ToFloat64(tr.jobsTotal.WithLabelValues("runner-q", "unknown", "unknown", "build", "unknown", "succeeded"))
	if count != 0 {
		t.Errorf("jobsTotal during replay = %v, want 0", count)
	}

	// After entering live mode, the next job should be counted.
	tr.EnterLiveMode()
	tr.HandleEvent(Event{Kind: EventOnline})
	tr.HandleEvent(Event{Kind: EventJobStarted, JobName: "build"})
	tr.HandleEvent(Event{Kind: EventJobCompleted, JobName: "build", Result: "succeeded"})

	count = testutil.ToFloat64(tr.jobsTotal.WithLabelValues("runner-q", "unknown", "unknown", "build", "unknown", "succeeded"))
	if count != 1 {
		t.Errorf("jobsTotal after live mode = %v, want 1", count)
	}
}

// TestTracker_UnknownLabels verifies "unknown" fallback labels when meta is absent.
func TestTracker_UnknownLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	tr := NewTracker("runner-q", reg)
	tr.HandleEvent(Event{Kind: EventOnline})
	tr.HandleEvent(Event{Kind: EventJobStarted, JobName: "build"})
	tr.HandleEvent(Event{Kind: EventJobCompleted, JobName: "build", Result: "succeeded"})

	count := testutil.ToFloat64(tr.jobsTotal.WithLabelValues("runner-q", "unknown", "unknown", "build", "unknown", "succeeded"))
	if count != 1 {
		t.Errorf("expected counter with unknown labels = 1, got %v", count)
	}
}

// TestTracker_MetaArrivesAfterStart verifies meta applied to in-progress job.
func TestTracker_MetaArrivesAfterStart(t *testing.T) {
	tr := newTestTracker(t)
	tr.HandleEvent(Event{Kind: EventOnline})
	tr.HandleEvent(Event{Kind: EventJobStarted, JobName: "build"})
	tr.SetWorkerMeta(WorkerMeta{Repo: "org/app", Workflow: "CI"})

	snap := tr.Snapshot()
	if snap.Current == nil {
		t.Fatal("current is nil")
	}
	if snap.Current.Repo != "org/app" {
		t.Errorf("Repo after late meta = %q, want %q", snap.Current.Repo, "org/app")
	}
}

// TestTracker_ConcurrentAccess verifies no data races under concurrent load.
func TestTracker_ConcurrentAccess(t *testing.T) {
	reg := prometheus.NewRegistry()
	tr := NewTracker("runner-q", reg)

	var wg sync.WaitGroup

	// Writer goroutine: simulate job events.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			tr.HandleEvent(Event{Kind: EventOnline})
			tr.SetWorkerMeta(WorkerMeta{Repo: "org/app", Workflow: "CI", JobName: "build"})
			tr.HandleEvent(Event{Kind: EventJobStarted, JobName: "build"})
			tr.HandleEvent(Event{Kind: EventJobCompleted, JobName: "build", Result: "succeeded"})
		}
	}()

	// Multiple reader goroutines: snapshot concurrently.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = tr.Snapshot()
			}
		}()
	}

	wg.Wait()
}
