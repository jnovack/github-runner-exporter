package collector

import (
	"strings"
	"testing"
	"time"

	"github.com/jnovack/github-runner-exporter/internal/runner"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func setup(t *testing.T) (*runner.Tracker, *runner.Config, *Collector, prometheus.Gatherer) {
	t.Helper()
	reg := prometheus.NewRegistry()
	cfg := &runner.Config{
		AgentName:  "runner-prod-01",
		PoolName:   "Default",
		WorkFolder: "_work",
	}
	tracker := runner.NewTracker(cfg.AgentName, reg)
	col := New(tracker, cfg, "v1.2.3", "abc1234", reg)
	return tracker, cfg, col, reg
}

func TestCollector_Offline(t *testing.T) {
	_, _, _, reg := setup(t)

	// No events fired — runner should be offline.
	count, err := testutil.GatherAndCount(reg)
	if err != nil {
		t.Fatalf("gather error: %v", err)
	}
	// At minimum info, online, busy should be present.
	if count < 3 {
		t.Errorf("expected at least 3 metrics, got %d", count)
	}

	err = testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP github_runner_busy 1 if the runner is currently executing a job, 0 otherwise.
# TYPE github_runner_busy gauge
github_runner_busy 0
# HELP github_runner_online 1 if the runner is online and listening for jobs, 0 otherwise.
# TYPE github_runner_online gauge
github_runner_online 0
`), "github_runner_busy", "github_runner_online")
	if err != nil {
		t.Error(err)
	}
}

func TestCollector_Idle(t *testing.T) {
	tracker, _, _, reg := setup(t)
	tracker.HandleEvent(runner.Event{Kind: runner.EventOnline, Timestamp: time.Now()})

	err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP github_runner_busy 1 if the runner is currently executing a job, 0 otherwise.
# TYPE github_runner_busy gauge
github_runner_busy 0
# HELP github_runner_online 1 if the runner is online and listening for jobs, 0 otherwise.
# TYPE github_runner_online gauge
github_runner_online 1
`), "github_runner_busy", "github_runner_online")
	if err != nil {
		t.Error(err)
	}
}

func TestCollector_Busy(t *testing.T) {
	tracker, _, _, reg := setup(t)
	tracker.HandleEvent(runner.Event{Kind: runner.EventOnline})
	tracker.HandleEvent(runner.Event{Kind: runner.EventJobStarted, JobName: "build"})

	err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP github_runner_busy 1 if the runner is currently executing a job, 0 otherwise.
# TYPE github_runner_busy gauge
github_runner_busy 1
# HELP github_runner_online 1 if the runner is online and listening for jobs, 0 otherwise.
# TYPE github_runner_online gauge
github_runner_online 1
`), "github_runner_busy", "github_runner_online")
	if err != nil {
		t.Error(err)
	}
}

func TestCollector_CurrentJobInfo(t *testing.T) {
	tracker, _, _, reg := setup(t)
	tracker.HandleEvent(runner.Event{Kind: runner.EventOnline})
	tracker.SetWorkerMeta(runner.WorkerMeta{
		Repo: "org/app", Workflow: "CI", RunID: "42", Actor: "alice", JobName: "build",
	})
	tracker.HandleEvent(runner.Event{Kind: runner.EventJobStarted, JobName: "build", Timestamp: time.Unix(1710489910, 0).UTC()})

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	var foundInfo, foundStart bool
	for _, mf := range metrics {
		switch mf.GetName() {
		case "github_runner_current_job_info":
			foundInfo = true
			m := mf.GetMetric()[0]
			labels := labelMap(m.GetLabel())
			if labels["repo"] != "org/app" {
				t.Errorf("current_job_info repo = %q, want %q", labels["repo"], "org/app")
			}
			if labels["workflow"] != "CI" {
				t.Errorf("current_job_info workflow = %q, want %q", labels["workflow"], "CI")
			}
			if labels["actor"] != "alice" {
				t.Errorf("current_job_info actor = %q, want %q", labels["actor"], "alice")
			}
			if _, exists := labels["run_id"]; exists {
				t.Error("current_job_info must not carry run_id label (unbounded cardinality)")
			}
			if m.GetGauge().GetValue() != 1 {
				t.Errorf("current_job_info value = %v, want 1", m.GetGauge().GetValue())
			}
		case "github_runner_current_job_start_timestamp_seconds":
			foundStart = true
			v := mf.GetMetric()[0].GetGauge().GetValue()
			if v != 1710489910 {
				t.Errorf("current_job_start_timestamp = %v, want 1710489910", v)
			}
		}
	}
	if !foundInfo {
		t.Error("github_runner_current_job_info not found")
	}
	if !foundStart {
		t.Error("github_runner_current_job_start_timestamp_seconds not found")
	}
}

func TestCollector_LastJobInfo(t *testing.T) {
	tracker, _, _, reg := setup(t)
	tracker.HandleEvent(runner.Event{Kind: runner.EventOnline})
	tracker.SetWorkerMeta(runner.WorkerMeta{
		Repo: "org/app", Workflow: "CI", RunID: "99", Actor: "bob", JobName: "test",
	})
	tracker.HandleEvent(runner.Event{Kind: runner.EventJobStarted, JobName: "test", Timestamp: time.Unix(1710489900, 0).UTC()})
	tracker.HandleEvent(runner.Event{Kind: runner.EventJobCompleted, JobName: "test", Result: "succeeded", Timestamp: time.Unix(1710489960, 0).UTC()})

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	var foundInfo, foundDuration, foundTimestamp bool
	for _, mf := range metrics {
		switch mf.GetName() {
		case "github_runner_last_job_info":
			foundInfo = true
			labels := labelMap(mf.GetMetric()[0].GetLabel())
			if labels["status"] != "succeeded" {
				t.Errorf("last_job_info status = %q, want %q", labels["status"], "succeeded")
			}
			if labels["repo"] != "org/app" {
				t.Errorf("last_job_info repo = %q, want %q", labels["repo"], "org/app")
			}
		case "github_runner_last_job_duration_seconds":
			foundDuration = true
			v := mf.GetMetric()[0].GetGauge().GetValue()
			if v != 60 {
				t.Errorf("last_job_duration_seconds = %v, want 60", v)
			}
		case "github_runner_last_job_timestamp_seconds":
			foundTimestamp = true
			v := mf.GetMetric()[0].GetGauge().GetValue()
			if v != 1710489960 {
				t.Errorf("last_job_timestamp_seconds = %v, want 1710489960", v)
			}
		}
	}
	if !foundInfo {
		t.Error("github_runner_last_job_info not found")
	}
	if !foundDuration {
		t.Error("github_runner_last_job_duration_seconds not found")
	}
	if !foundTimestamp {
		t.Error("github_runner_last_job_timestamp_seconds not found")
	}
}

func TestCollector_NoCurrent_WhenIdle(t *testing.T) {
	tracker, _, _, reg := setup(t)
	tracker.HandleEvent(runner.Event{Kind: runner.EventOnline})
	tracker.HandleEvent(runner.Event{Kind: runner.EventJobStarted, JobName: "build"})
	tracker.HandleEvent(runner.Event{Kind: runner.EventJobCompleted, JobName: "build", Result: "succeeded"})

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range metrics {
		if mf.GetName() == "github_runner_current_job_info" {
			t.Error("github_runner_current_job_info should not be present when idle")
		}
	}
}

func TestCollector_JobCounterLabels(t *testing.T) {
	tracker, _, _, reg := setup(t)
	tracker.HandleEvent(runner.Event{Kind: runner.EventOnline})
	tracker.SetWorkerMeta(runner.WorkerMeta{
		Repo: "org/app", Workflow: "CI", RunID: "1", Actor: "alice", JobName: "build",
	})
	tracker.HandleEvent(runner.Event{Kind: runner.EventJobStarted, JobName: "build"})
	tracker.HandleEvent(runner.Event{Kind: runner.EventJobCompleted, JobName: "build", Result: "succeeded"})

	count := testutil.ToFloat64(
		// Access the counter directly via tracker — we need to reach the registered counter.
		// Use GatherAndCompare to check the label set is correct.
		prometheus.NewGauge(prometheus.GaugeOpts{}), // placeholder; real check below
	)
	_ = count

	metrics, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range metrics {
		if mf.GetName() != "github_runner_jobs_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := labelMap(m.GetLabel())
			if labels["repo"] != "org/app" {
				t.Errorf("jobs_total repo = %q, want %q", labels["repo"], "org/app")
			}
			if labels["workflow"] != "CI" {
				t.Errorf("jobs_total workflow = %q, want %q", labels["workflow"], "CI")
			}
			if labels["job_name"] != "build" {
				t.Errorf("jobs_total job_name = %q, want %q", labels["job_name"], "build")
			}
			status := labels["status"]
			if status != "succeeded" && status != "failed" && status != "cancelled" {
				t.Errorf("jobs_total status = %q, want one of succeeded|failed|cancelled", status)
			}
			if labels["runner_name"] != "runner-prod-01" {
				t.Errorf("jobs_total runner_name = %q, want %q", labels["runner_name"], "runner-prod-01")
			}
			value := m.GetCounter().GetValue()
			if status == "succeeded" && value != 1 {
				t.Errorf("jobs_total value for succeeded = %v, want 1", value)
			}
			if (status == "failed" || status == "cancelled") && value != 0 {
				t.Errorf("jobs_total value for %s = %v, want 0", status, value)
			}
		}
	}
}

func TestStateToGauges(t *testing.T) {
	tests := []struct {
		state      runner.State
		wantOnline float64
		wantBusy   float64
	}{
		{runner.StateOffline, 0, 0},
		{runner.StateIdle, 1, 0},
		{runner.StateBusy, 1, 1},
	}
	for _, tt := range tests {
		online, busy := stateToGauges(tt.state)
		if online != tt.wantOnline || busy != tt.wantBusy {
			t.Errorf("stateToGauges(%v) = (%v, %v), want (%v, %v)",
				tt.state, online, busy, tt.wantOnline, tt.wantBusy)
		}
	}
}

// labelMap converts a slice of LabelPair to a plain map for easy test assertions.
func labelMap(pairs []*dto.LabelPair) map[string]string {
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		m[p.GetName()] = p.GetValue()
	}
	return m
}
