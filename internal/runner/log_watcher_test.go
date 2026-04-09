package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/prometheus/client_golang/prometheus"
)

func newTestTrackerFor(t *testing.T, runnerName string) *Tracker {
	t.Helper()
	return NewTracker(runnerName, prometheus.NewRegistry())
}

// TestIsRunnerLog / TestIsWorkerLog check the filename predicates.
func TestIsRunnerLog(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"Runner_20240315-080000-utc.log", true},
		{"runner_20240315.log", false},
		{"Worker_20240315-080000-utc.log", false},
		{"Runner_20240315.txt", false},
		{"Runner_.log", true},
	}
	for _, tt := range tests {
		if got := isRunnerLog(tt.name); got != tt.want {
			t.Errorf("isRunnerLog(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestIsWorkerLog(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"Worker_20240315-080000-utc.log", true},
		{"worker_20240315.log", false},
		{"Runner_20240315-080000-utc.log", false},
		{"Worker_.log", true},
	}
	for _, tt := range tests {
		if got := isWorkerLog(tt.name); got != tt.want {
			t.Errorf("isWorkerLog(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestNewestRunnerLog_Empty verifies empty string for empty directory.
func TestNewestRunnerLog_Empty(t *testing.T) {
	dir := t.TempDir()
	w := NewWatcher(dir, newTestTrackerFor(t, "runner"))
	if got := w.newestRunnerLog(); got != "" {
		t.Errorf("newestRunnerLog() = %q, want empty for empty dir", got)
	}
}

// TestNewestRunnerLog_Single returns the only file present.
func TestNewestRunnerLog_Single(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "Runner_20240315-080000-utc.log")
	if err := os.WriteFile(p, []byte("[2024-03-15 08:00:02Z INFO Runner] Listening for Jobs\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w := NewWatcher(dir, newTestTrackerFor(t, "runner"))
	if got := w.newestRunnerLog(); got != p {
		t.Errorf("newestRunnerLog() = %q, want %q", got, p)
	}
}

// TestNewestRunnerLog_Multiple returns the most recently modified file.
func TestNewestRunnerLog_Multiple(t *testing.T) {
	dir := t.TempDir()

	older := filepath.Join(dir, "Runner_20240315-080000-utc.log")
	newer := filepath.Join(dir, "Runner_20240315-100000-utc.log")

	if err := os.WriteFile(older, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond) // ensure different mtime
	if err := os.WriteFile(newer, []byte("new\n"), 0600); err != nil {
		t.Fatal(err)
	}

	w := NewWatcher(dir, newTestTrackerFor(t, "runner"))
	if got := w.newestRunnerLog(); got != newer {
		t.Errorf("newestRunnerLog() = %q, want %q (newest)", got, newer)
	}
}

// TestReplay_IdleState verifies that replaying a log that ends on "Listening for Jobs"
// results in StateIdle.
func TestReplay_IdleState(t *testing.T) {
	dir := t.TempDir()
	tracker := newTestTrackerFor(t, "runner")

	logPath := filepath.Join(dir, "Runner_typical.log")
	content, err := os.ReadFile(filepath.Join("..", "..", "test", "Runner_typical.log"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(logPath, content, 0600); err != nil {
		t.Fatal(err)
	}

	w := NewWatcher(dir, tracker)
	w.replayRunnerLog(logPath)

	snap := tracker.Snapshot()
	if snap.State != StateIdle {
		t.Errorf("state after replay = %v, want StateIdle", snap.State)
	}
}

// TestStartup_ReplayThenWalkEnrichesLast verifies the full startup sequence:
// replaying the Runner log (which leaves last with no metadata) followed by
// walking existing Worker logs and enriching last should populate repo/workflow.
// This is the scenario that caused all metadata to appear as "unknown" on startup.
func TestStartup_ReplayThenWalkEnrichesLast(t *testing.T) {
	dir := t.TempDir()
	tracker := newTestTrackerFor(t, "runner")

	// Runner log: one completed job, runner now idle.
	runnerContent, err := os.ReadFile(filepath.Join("..", "..", "test", "Runner_typical.log"))
	if err != nil {
		t.Fatalf("read Runner fixture: %v", err)
	}
	runnerLog := filepath.Join(dir, "Runner_20240315-080000-utc.log")
	if err := os.WriteFile(runnerLog, runnerContent, 0600); err != nil {
		t.Fatal(err)
	}

	// Worker log: metadata for the most recent job.
	workerContent, err := os.ReadFile(filepath.Join("..", "..", "test", "Worker_succeeded.log"))
	if err != nil {
		t.Fatalf("read Worker fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Worker_20240315-080510-utc.log"), workerContent, 0600); err != nil {
		t.Fatal(err)
	}

	w := NewWatcher(dir, tracker)

	// Simulate the startup sequence from watcher.Run.
	tracker.EnterReplayMode()
	w.replayRunnerLog(runnerLog)
	WalkExistingWorkerLogs(dir, tracker, 0)
	tracker.EnrichLastFromPendingMeta()
	tracker.EnterLiveMode()

	snap := tracker.Snapshot()
	if snap.Last == nil {
		t.Fatal("expected last job after replay, got nil")
	}
	if snap.Last.Repo != "octocat/hello-world" {
		t.Errorf("Last.Repo = %q, want %q", snap.Last.Repo, "octocat/hello-world")
	}
	if snap.Last.Workflow != "CI" {
		t.Errorf("Last.Workflow = %q, want %q", snap.Last.Workflow, "CI")
	}
	if snap.Last.Actor != "monalisa" {
		t.Errorf("Last.Actor = %q, want %q", snap.Last.Actor, "monalisa")
	}
	if snap.Last.RunID != "9000000001" {
		t.Errorf("Last.RunID = %q, want %q", snap.Last.RunID, "9000000001")
	}
}

// TestTailRunnerLog_IncrementalReads verifies offset tracking across incremental reads.
func TestTailRunnerLog_IncrementalReads(t *testing.T) {
	dir := t.TempDir()
	tracker := newTestTrackerFor(t, "runner")
	logPath := filepath.Join(dir, "Runner_20240315-080000-utc.log")

	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	w := NewWatcher(dir, tracker)

	// First read: write and tail one "Listening" line.
	if _, err := f.WriteString("[2024-03-15 08:00:02Z INFO Runner] Listening for Jobs\n"); err != nil {
		t.Fatal(err)
	}
	offset := w.tailRunnerLog(logPath, 0)
	if offset == 0 {
		t.Error("offset should be non-zero after reading first line")
	}
	if tracker.Snapshot().State != StateIdle {
		t.Error("expected StateIdle after first tail")
	}

	// Second read: add a job start line.
	if _, err := f.WriteString("[2024-03-15 08:05:10Z INFO Runner] Running job: build\n"); err != nil {
		t.Fatal(err)
	}
	offset = w.tailRunnerLog(logPath, offset)

	if tracker.Snapshot().State != StateBusy {
		t.Error("expected StateBusy after job start line")
	}

	// Third read: add a completion line.
	if _, err := f.WriteString("[2024-03-15 08:07:45Z INFO Runner] Job build completed with result: Succeeded\n"); err != nil {
		t.Fatal(err)
	}
	w.tailRunnerLog(logPath, offset)

	if tracker.Snapshot().State != StateIdle {
		t.Error("expected StateIdle after job completion")
	}
}

// TestReadWorkerLog_SetsTrackerMeta verifies worker log reading updates tracker.
func TestReadWorkerLog_SetsTrackerMeta(t *testing.T) {
	dir := t.TempDir()
	tracker := newTestTrackerFor(t, "runner")

	workerPath := filepath.Join(dir, "Worker_20240315-080510-utc.log")
	content, err := os.ReadFile(filepath.Join("..", "..", "test", "Worker_succeeded.log"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(workerPath, content, 0600); err != nil {
		t.Fatal(err)
	}

	w := NewWatcher(dir, tracker)
	w.readWorkerLog(workerPath)

	// Trigger a job start so meta is applied.
	tracker.HandleEvent(Event{Kind: EventJobStarted, JobName: "build"})
	snap := tracker.Snapshot()
	if snap.Current == nil {
		t.Fatal("current is nil")
	}
	if snap.Current.Repo != "octocat/hello-world" {
		t.Errorf("Repo = %q, want %q", snap.Current.Repo, "octocat/hello-world")
	}
}

// TestWatcher_Run_CancelContext verifies Run exits cleanly on context cancellation.
func TestWatcher_Run_CancelContext(t *testing.T) {
	dir := t.TempDir()
	tracker := newTestTrackerFor(t, "runner")
	w := NewWatcher(dir, tracker)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- w.Run(ctx)
	}()

	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("Run did not exit within 3 seconds of context cancellation")
	}
}

// TestWalkExistingWorkerLogs verifies pre-existing Worker logs are parsed on startup.
func TestWalkExistingWorkerLogs(t *testing.T) {
	dir := t.TempDir()
	tracker := newTestTrackerFor(t, "runner")

	content, err := os.ReadFile(filepath.Join("..", "..", "test", "Worker_succeeded.log"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Worker_20240315-080510-utc.log"), content, 0600); err != nil {
		t.Fatal(err)
	}

	WalkExistingWorkerLogs(dir, tracker, 0)

	// Trigger a job so meta is applied.
	tracker.HandleEvent(Event{Kind: EventJobStarted, JobName: "build"})
	snap := tracker.Snapshot()
	if snap.Current == nil || snap.Current.Repo != "octocat/hello-world" {
		t.Errorf("Repo after WalkExistingWorkerLogs = %q, want %q",
			func() string {
				if snap.Current == nil {
					return "<nil>"
				}
				return snap.Current.Repo
			}(), "octocat/hello-world")
	}
}

// TestWalkExistingWorkerLogs_EmptyDir verifies no panic on empty directory.
func TestWalkExistingWorkerLogs_EmptyDir(t *testing.T) {
	tracker := newTestTrackerFor(t, "runner")
	WalkExistingWorkerLogs(t.TempDir(), tracker, 0) // should not panic
}

// TestWalkExistingWorkerLogs_NonWorkerFilesIgnored verifies non-Worker files are skipped.
func TestWalkExistingWorkerLogs_NonWorkerFilesIgnored(t *testing.T) {
	dir := t.TempDir()
	tracker := newTestTrackerFor(t, "runner")
	// Write files that should be ignored.
	if err := os.WriteFile(filepath.Join(dir, "Runner_test.log"), []byte("ignore me\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "not-a-log.txt"), []byte("also ignored\n"), 0600); err != nil {
		t.Fatal(err)
	}
	WalkExistingWorkerLogs(dir, tracker, 0)
	// State should remain offline — no meta should have been applied.
	if tracker.Snapshot().State != StateOffline {
		t.Error("state should remain offline when no Worker logs are present")
	}
}

// TestReadWorkerLog_NonExistent verifies readWorkerLog is a no-op for missing files.
func TestReadWorkerLog_NonExistent(t *testing.T) {
	dir := t.TempDir()
	tracker := newTestTrackerFor(t, "runner")
	w := NewWatcher(dir, tracker)
	// Should not panic.
	w.readWorkerLog(filepath.Join(dir, "Worker_does_not_exist.log"))
}

// TestHandleFSEvent_NewRunnerLog verifies that a Create event for a Runner log
// triggers a switch to the new file and a tail.
func TestHandleFSEvent_NewRunnerLog(t *testing.T) {
	dir := t.TempDir()
	tracker := newTestTrackerFor(t, "runner")
	w := NewWatcher(dir, tracker)

	// Write a Runner log.
	logPath := filepath.Join(dir, "Runner_20240315-100000-utc.log")
	if err := os.WriteFile(logPath, []byte("[2024-03-15 10:00:01Z INFO Runner] Listening for Jobs\n"), 0600); err != nil {
		t.Fatal(err)
	}

	runnerLog := ""
	var offset int64

	w.handleFSEvent(
		fsnotify.Event{Name: logPath, Op: fsnotify.Create},
		&runnerLog, &offset,
	)

	if runnerLog != logPath {
		t.Errorf("runnerLog = %q, want %q", runnerLog, logPath)
	}
	if tracker.Snapshot().State != StateIdle {
		t.Errorf("state after Create event = %v, want StateIdle", tracker.Snapshot().State)
	}
}

// TestHandleFSEvent_WriteRunnerLog verifies Write events are tailed.
func TestHandleFSEvent_WriteRunnerLog(t *testing.T) {
	dir := t.TempDir()
	tracker := newTestTrackerFor(t, "runner")
	w := NewWatcher(dir, tracker)

	logPath := filepath.Join(dir, "Runner_test.log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	_, _ = f.WriteString("[2024-03-15 10:00:01Z INFO Runner] Listening for Jobs\n")

	runnerLog := logPath
	var offset int64

	w.handleFSEvent(
		fsnotify.Event{Name: logPath, Op: fsnotify.Write},
		&runnerLog, &offset,
	)

	if tracker.Snapshot().State != StateIdle {
		t.Errorf("state after Write event = %v, want StateIdle", tracker.Snapshot().State)
	}
}

// TestHandleFSEvent_NewWorkerLog verifies Create events for Worker logs trigger metadata parse.
func TestHandleFSEvent_NewWorkerLog(t *testing.T) {
	dir := t.TempDir()
	tracker := newTestTrackerFor(t, "runner")
	w := NewWatcher(dir, tracker)

	content, err := os.ReadFile(filepath.Join("..", "..", "test", "Worker_succeeded.log"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	workerPath := filepath.Join(dir, "Worker_20240315-080510-utc.log")
	if err := os.WriteFile(workerPath, content, 0600); err != nil {
		t.Fatal(err)
	}

	runnerLog := ""
	var offset int64
	w.handleFSEvent(
		fsnotify.Event{Name: workerPath, Op: fsnotify.Create},
		&runnerLog, &offset,
	)

	tracker.HandleEvent(Event{Kind: EventJobStarted, JobName: "build"})
	snap := tracker.Snapshot()
	if snap.Current == nil || snap.Current.Repo != "octocat/hello-world" {
		t.Error("worker meta not applied after Create event for Worker log")
	}
}

// TestHandleFSEvent_WorkerLog_WriteEvent verifies that Write events for Worker
// logs are also processed, covering the case where the file is created before
// the metadata lines have been written.
func TestHandleFSEvent_WorkerLog_WriteEvent(t *testing.T) {
	dir := t.TempDir()
	tracker := newTestTrackerFor(t, "runner")
	w := NewWatcher(dir, tracker)

	content, err := os.ReadFile(filepath.Join("..", "..", "test", "Worker_succeeded.log"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	workerPath := filepath.Join(dir, "Worker_20240315-080510-utc.log")

	// Simulate a Create event with only the first line written (no metadata yet).
	if err := os.WriteFile(workerPath, content[:50], 0600); err != nil {
		t.Fatal(err)
	}
	runnerLog := ""
	var offset int64
	w.handleFSEvent(fsnotify.Event{Name: workerPath, Op: fsnotify.Create}, &runnerLog, &offset)

	// No metadata yet — current job should have no repo.
	tracker.HandleEvent(Event{Kind: EventJobStarted, JobName: "build"})
	snap := tracker.Snapshot()
	if snap.Current != nil && snap.Current.Repo != "" {
		t.Error("expected no repo metadata from partial Worker log on Create")
	}

	// Simulate a Write event after the full content has been flushed.
	if err := os.WriteFile(workerPath, content, 0600); err != nil {
		t.Fatal(err)
	}
	w.handleFSEvent(fsnotify.Event{Name: workerPath, Op: fsnotify.Write}, &runnerLog, &offset)

	snap = tracker.Snapshot()
	if snap.Current == nil || snap.Current.Repo != "octocat/hello-world" {
		t.Error("expected repo metadata to be applied after Write event with full content")
	}
}

// TestHandleFSEvent_WorkerLog_HarvestedSkipsReread verifies that once a Worker
// log has been fully harvested, subsequent Write events are skipped.
func TestHandleFSEvent_WorkerLog_HarvestedSkipsReread(t *testing.T) {
	dir := t.TempDir()
	tracker := newTestTrackerFor(t, "runner")
	w := NewWatcher(dir, tracker)

	content, err := os.ReadFile(filepath.Join("..", "..", "test", "Worker_succeeded.log"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	workerPath := filepath.Join(dir, "Worker_20240315-080510-utc.log")
	if err := os.WriteFile(workerPath, content, 0600); err != nil {
		t.Fatal(err)
	}

	runnerLog := ""
	var offset int64

	// First Create event — should harvest the file and mark it as done.
	w.handleFSEvent(fsnotify.Event{Name: workerPath, Op: fsnotify.Create}, &runnerLog, &offset)
	if !w.harvestedLogs[workerPath] {
		t.Fatal("expected workerPath to be marked as harvested after full metadata read")
	}

	// Remove the file to prove subsequent Write events do NOT re-read it.
	if err := os.Remove(workerPath); err != nil {
		t.Fatal(err)
	}
	// This would panic or error if readWorkerLog tried to open the deleted file.
	w.handleFSEvent(fsnotify.Event{Name: workerPath, Op: fsnotify.Write}, &runnerLog, &offset)
	// If we reach here without error, the harvested guard worked.
}

// TestWatcher_WindowsPaths verifies path construction uses filepath.Join (cross-platform).
func TestWatcher_WindowsPaths(t *testing.T) {
	// Construct a dir path the same way the watcher would on any OS.
	base := t.TempDir()
	diagDir := filepath.Join(base, "_diag")
	if err := os.MkdirAll(diagDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a Runner log using platform-appropriate path.
	logPath := filepath.Join(diagDir, "Runner_test.log")
	if err := os.WriteFile(logPath, []byte("[2024-03-15 08:00:02Z INFO Runner] Listening for Jobs\n"), 0600); err != nil {
		t.Fatal(err)
	}

	w := NewWatcher(diagDir, newTestTrackerFor(t, "runner"))
	got := w.newestRunnerLog()
	// The returned path should use the platform separator (filepath.Join was used internally).
	if got != logPath {
		t.Errorf("newestRunnerLog() = %q, want %q", got, logPath)
	}
}
