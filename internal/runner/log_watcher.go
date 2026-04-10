package runner

import (
	"bufio"
	"context"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher monitors the runner's _diag directory, tails the active Runner_*.log
// file, and reads new Worker_*.log files as they appear.
//
// It sends parsed Events and WorkerMeta values to the provided Tracker.
type Watcher struct {
	diagDir       string
	tracker       *Tracker
	poll          time.Duration   // fallback poll interval when fsnotify misses events
	walkWindow    time.Duration   // how far back to scan Worker logs on startup; 0 = unlimited
	harvestedLogs map[string]bool // Worker log paths where full metadata was obtained
}

// NewWatcher creates a Watcher for the given _diag directory.
// The walk window defaults to 90 days; override with SetWalkWindow.
func NewWatcher(diagDir string, tracker *Tracker) *Watcher {
	return &Watcher{
		diagDir:       diagDir,
		tracker:       tracker,
		poll:          5 * time.Second,
		walkWindow:    90 * 24 * time.Hour,
		harvestedLogs: make(map[string]bool),
	}
}

// SetWalkWindow overrides the window of Worker logs scanned on startup for label
// pre-seeding. A value of 0 disables the time filter (scan all files).
func (w *Watcher) SetWalkWindow(d time.Duration) {
	w.walkWindow = d
}

// Run starts watching the diag directory until ctx is cancelled.
// It blocks; call in a goroutine.
func (w *Watcher) Run(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	if err := watcher.Add(w.diagDir); err != nil {
		return err
	}

	// Replay historical log events for state only — counters are suppressed
	// during replay because Worker log metadata is not yet available.
	w.tracker.EnterReplayMode()
	runnerLog := w.newestRunnerLog()
	var runnerOffset int64
	if runnerLog != "" {
		runnerOffset = w.replayRunnerLog(runnerLog)
	}
	// Walk existing Worker logs so in-progress and last-completed jobs get metadata,
	// and pre-seed label cardinalities from historical jobs.
	WalkExistingWorkerLogs(w.diagDir, w.tracker, w.walkWindow)
	// Enrich last job info with the most recent Worker log metadata.
	w.tracker.EnrichLastFromPendingMeta()
	// From this point on, completed jobs are recorded in counters/histograms.
	w.tracker.EnterLiveMode()

	pollTicker := time.NewTicker(w.poll)
	defer pollTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			w.handleFSEvent(event, &runnerLog, &runnerOffset)

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			slog.Warn("fsnotify error", "err", err)

		case <-pollTicker.C:
			// Poll for new Runner log files in case fsnotify missed a rename.
			if newest := w.newestRunnerLog(); newest != "" && newest != runnerLog {
				slog.Info("runner log rotated (poll)", "new", newest)
				runnerLog = newest
				runnerOffset = 0
			}
			if runnerLog != "" {
				runnerOffset = w.tailRunnerLog(runnerLog, runnerOffset)
			}
		}
	}
}

// handleFSEvent processes a single fsnotify event.
func (w *Watcher) handleFSEvent(event fsnotify.Event, runnerLog *string, runnerOffset *int64) {
	name := filepath.Base(event.Name)

	if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) {
		switch {
		case isRunnerLog(name):
			// New Runner log file created (rotation after runner restart).
			if event.Has(fsnotify.Create) && event.Name != *runnerLog {
				slog.Info("runner log rotated", "new", event.Name)
				*runnerLog = event.Name
				*runnerOffset = 0
			}
			*runnerOffset = w.tailRunnerLog(*runnerLog, *runnerOffset)

		case isWorkerLog(name):
			// Read on both Create and Write: the metadata fields (actor, workflow,
			// repository) appear ~600 lines into the Worker log, inside the job
			// message JSON. The Create event fires before those lines are written,
			// so we must re-read on subsequent Write events until we have full
			// metadata. harvestedLogs tracks files where we already got everything.
			w.readWorkerLog(event.Name)
		}
	}
}

// replayRunnerLog reads a Runner log from the start to reconstruct current state.
// Returns the file offset after replay.
func (w *Watcher) replayRunnerLog(path string) int64 {
	slog.Debug("replaying runner log", "path", path)
	return w.tailRunnerLog(path, 0)
}

// tailRunnerLog reads new content from path starting at offset, parses events,
// and returns the new offset.
func (w *Watcher) tailRunnerLog(path string, offset int64) int64 {
	f, err := os.Open(path) // #nosec G304 — path is constructed from trusted config
	if err != nil {
		slog.Warn("open runner log", "path", path, "err", err)
		return offset
	}
	defer f.Close()

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			slog.Warn("seek runner log", "path", path, "err", err)
			return offset
		}
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if ev, ok := ParseLine(line); ok {
			w.tracker.HandleEvent(ev)
		}
	}

	newOffset, _ := f.Seek(0, io.SeekCurrent)
	return newOffset
}

// readWorkerLog reads an entire Worker log file and sends metadata to the tracker.
// Once all five metadata fields are obtained from a file, it is marked in
// harvestedLogs and subsequent Write events for that file are skipped.
func (w *Watcher) readWorkerLog(path string) {
	if w.harvestedLogs[path] {
		return
	}
	slog.Debug("reading worker log", "path", path)
	data, err := os.ReadFile(path) // #nosec G304 — path comes from fsnotify in trusted dir
	if err != nil {
		slog.Warn("read worker log", "path", path, "err", err)
		return
	}
	meta := ParseWorkerLog(string(data))
	if meta.Repo != "" || meta.JobName != "" || !meta.StartedAt.IsZero() || !meta.EndedAt.IsZero() {
		w.tracker.SetWorkerMeta(meta)
		if meta.Repo != "" && meta.Workflow != "" && meta.RunID != "" && meta.Actor != "" && meta.JobName != "" &&
			!meta.StartedAt.IsZero() && !meta.EndedAt.IsZero() {
			w.harvestedLogs[path] = true
		}
	}
}

// newestRunnerLog returns the path of the most-recently-modified Runner_*.log
// in the diag directory, or "" if none exist.
func (w *Watcher) newestRunnerLog() string {
	entries, err := os.ReadDir(w.diagDir)
	if err != nil {
		return ""
	}

	type candidate struct {
		path    string
		modTime time.Time
	}
	var found []candidate

	for _, e := range entries {
		if e.IsDir() || !isRunnerLog(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		found = append(found, candidate{
			path:    filepath.Join(w.diagDir, e.Name()),
			modTime: info.ModTime(),
		})
	}

	if len(found) == 0 {
		return ""
	}

	sort.Slice(found, func(i, j int) bool {
		return found[i].modTime.After(found[j].modTime)
	})
	return found[0].path
}

// isRunnerLog reports whether name is a Runner diagnostic log filename.
func isRunnerLog(name string) bool {
	return strings.HasPrefix(name, "Runner_") && strings.HasSuffix(name, ".log")
}

// isWorkerLog reports whether name is a Worker diagnostic log filename.
func isWorkerLog(name string) bool {
	return strings.HasPrefix(name, "Worker_") && strings.HasSuffix(name, ".log")
}

// WalkExistingWorkerLogs reads all Worker_*.log files already present in diagDir.
// Useful for initial state reconstruction (metadata only; no duplicate events).
//
// walkWindow limits how far back in time to look: files older than
// time.Now().Add(-walkWindow) are skipped. Pass 0 to scan all files.
//
// For each complete Worker log (all five metadata fields present), the label
// combination is pre-seeded in the tracker so it survives across restarts.
func WalkExistingWorkerLogs(diagDir string, tracker *Tracker, walkWindow time.Duration) {
	cutoff := time.Time{}
	if walkWindow > 0 {
		cutoff = time.Now().Add(-walkWindow)
	}

	_ = filepath.WalkDir(diagDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isWorkerLog(d.Name()) {
			return nil
		}
		if !cutoff.IsZero() {
			info, ierr := d.Info()
			if ierr != nil || info.ModTime().Before(cutoff) {
				return nil
			}
		}
		data, rerr := os.ReadFile(path) // #nosec G304
		if rerr != nil {
			return nil
		}
		meta := ParseWorkerLog(string(data))
		if meta.Repo != "" || meta.JobName != "" || !meta.StartedAt.IsZero() || !meta.EndedAt.IsZero() {
			tracker.SetWorkerMeta(meta)
		}
		if meta.Repo != "" && meta.Workflow != "" && meta.Actor != "" && meta.JobName != "" {
			tracker.PreseedJobLabels(meta.Repo, meta.Workflow, meta.JobName, meta.Actor)
		}
		return nil
	})
}
