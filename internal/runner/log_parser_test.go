package runner

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseLine(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantOK     bool
		wantKind   EventKind
		wantJob    string
		wantResult string
		wantTime   time.Time
	}{
		{
			name:     "online event",
			line:     "[2024-03-15 08:00:02Z INFO Terminal] WRITE LINE: 2024-03-15 08:00:02Z: Listening for Jobs",
			wantOK:   true,
			wantKind: EventOnline,
			wantTime: time.Date(2024, 3, 15, 8, 0, 2, 0, time.UTC),
		},
		{
			name:     "job started",
			line:     "[2024-03-15 08:05:10Z INFO Terminal] WRITE LINE: 2024-03-15 08:05:10Z: Running job: build",
			wantOK:   true,
			wantKind: EventJobStarted,
			wantJob:  "build",
			wantTime: time.Date(2024, 3, 15, 8, 5, 10, 0, time.UTC),
		},
		{
			name:     "job dispatcher request received treated as start fallback",
			line:     "[2026-04-09 13:01:50Z INFO JobDispatcher] Job request 0 for plan e6f2e18a-f6cf-472b-b888-07b5855d400b job 3efb7d06-7227-5246-b53d-1d9e62b30941 received.",
			wantOK:   true,
			wantKind: EventJobStarted,
			wantTime: time.Date(2026, 4, 9, 13, 1, 50, 0, time.UTC),
		},
		{
			name:       "job completed succeeded",
			line:       "[2024-03-15 08:07:45Z INFO Terminal] WRITE LINE: 2024-03-15 08:07:45Z: Job build completed with result: Succeeded",
			wantOK:     true,
			wantKind:   EventJobCompleted,
			wantJob:    "build",
			wantResult: "succeeded",
			wantTime:   time.Date(2024, 3, 15, 8, 7, 45, 0, time.UTC),
		},
		{
			name:       "job completed failed",
			line:       "[2024-03-15 08:18:30Z INFO Terminal] WRITE LINE: 2024-03-15 08:18:30Z: Job test completed with result: Failed",
			wantOK:     true,
			wantKind:   EventJobCompleted,
			wantJob:    "test",
			wantResult: "failed",
		},
		{
			// Modern runners (≥2.333.1) emit "Canceled" (one l); older runners emit
			// "Cancelled" (two l's). ParseLine normalizes both to "canceled".
			name:       "job completed canceled",
			line:       "[2024-03-15 09:03:12Z INFO Terminal] WRITE LINE: 2024-03-15 09:03:12Z: Job deploy completed with result: Canceled",
			wantOK:     true,
			wantKind:   EventJobCompleted,
			wantJob:    "deploy",
			wantResult: "canceled",
		},
		{
			name:       "job completed cancelled normalizes to canceled",
			line:       "[2024-03-15 09:03:12Z INFO Terminal] WRITE LINE: 2024-03-15 09:03:12Z: Job deploy completed with result: Cancelled",
			wantOK:     true,
			wantKind:   EventJobCompleted,
			wantJob:    "deploy",
			wantResult: "canceled",
		},
		{
			name:     "job name with spaces",
			line:     "[2024-03-15 10:00:00Z INFO Terminal] WRITE LINE: 2024-03-15 10:00:00Z: Running job: build and test",
			wantOK:   true,
			wantKind: EventJobStarted,
			wantJob:  "build and test",
		},
		{
			name:       "job name with spaces completed",
			line:       "[2024-03-15 10:05:00Z INFO Terminal] WRITE LINE: 2024-03-15 10:05:00Z: Job build and test completed with result: Succeeded",
			wantOK:     true,
			wantKind:   EventJobCompleted,
			wantJob:    "build and test",
			wantResult: "succeeded",
		},
		{
			name:       "matrix job name with slash",
			line:       "[2024-03-15 10:05:00Z INFO Terminal] WRITE LINE: 2024-03-15 10:05:00Z: Job build-and-deploy / deploy-in-azure completed with result: Succeeded",
			wantOK:     true,
			wantKind:   EventJobCompleted,
			wantJob:    "build-and-deploy / deploy-in-azure",
			wantResult: "succeeded",
		},
		{
			name:   "unrecognized message",
			line:   "[2024-03-15 08:00:01Z INFO HostContext] Well known directory 'Bin': '/actions-runner/bin.2.333.1'",
			wantOK: false,
		},
		{
			name:   "empty line",
			line:   "",
			wantOK: false,
		},
		{
			name:   "garbage line",
			line:   "not a log line at all",
			wantOK: false,
		},
		{
			name:   "truncated line",
			line:   "[2024-03-15 08:00:01Z INFO",
			wantOK: false,
		},
		{
			name:   "invalid timestamp digits",
			line:   "[XXXX-XX-XX XX:XX:XXZ INFO Terminal] WRITE LINE: XXXX-XX-XX XX:XX:XXZ: Listening for Jobs",
			wantOK: false,
		},
		{
			name:     "windows CRLF — online event",
			line:     "[2024-03-15 08:00:02Z INFO Terminal] WRITE LINE: 2024-03-15 08:00:02Z: Listening for Jobs\r",
			wantOK:   true,
			wantKind: EventOnline,
		},
		{
			name:     "windows CRLF — job started",
			line:     "[2024-03-15 08:05:10Z INFO Terminal] WRITE LINE: 2024-03-15 08:05:10Z: Running job: build\r",
			wantOK:   true,
			wantKind: EventJobStarted,
			wantJob:  "build",
		},
		{
			name:       "windows CRLF — job completed",
			line:       "[2024-03-15 08:07:45Z INFO Terminal] WRITE LINE: 2024-03-15 08:07:45Z: Job build completed with result: Succeeded\r",
			wantOK:     true,
			wantKind:   EventJobCompleted,
			wantJob:    "build",
			wantResult: "succeeded",
		},
		{
			name:     "write line without embedded console timestamp",
			line:     "[2024-03-15 08:05:10Z INFO Terminal] WRITE LINE: Running job: build",
			wantOK:   true,
			wantKind: EventJobStarted,
			wantJob:  "build",
		},
		{
			name:       "write line without embedded console timestamp completed",
			line:       "[2024-03-15 08:07:45Z INFO Terminal] WRITE LINE: Job build completed with result: Succeeded",
			wantOK:     true,
			wantKind:   EventJobCompleted,
			wantJob:    "build",
			wantResult: "succeeded",
		},
		{
			name:     "component with punctuation",
			line:     "[2024-03-15 08:00:02Z INFO Runner.Listener-1] WRITE LINE: 2024-03-15 08:00:02Z: Listening for Jobs",
			wantOK:   true,
			wantKind: EventOnline,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseLine(tt.line)
			if ok != tt.wantOK {
				t.Fatalf("ParseLine(%q) ok = %v, want %v", tt.line, ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got.Kind != tt.wantKind {
				t.Errorf("Kind = %v, want %v", got.Kind, tt.wantKind)
			}
			if tt.wantJob != "" && got.JobName != tt.wantJob {
				t.Errorf("JobName = %q, want %q", got.JobName, tt.wantJob)
			}
			if tt.wantResult != "" && got.Result != tt.wantResult {
				t.Errorf("Result = %q, want %q", got.Result, tt.wantResult)
			}
			if !tt.wantTime.IsZero() && !got.Timestamp.Equal(tt.wantTime) {
				t.Errorf("Timestamp = %v, want %v", got.Timestamp, tt.wantTime)
			}
		})
	}
}

func TestParseWorkerLog_Succeeded(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "test", "Worker_succeeded.log"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	meta := ParseWorkerLog(string(content))
	if meta.Repo != "octocat/hello-world" {
		t.Errorf("Repo = %q, want %q", meta.Repo, "octocat/hello-world")
	}
	if meta.Workflow != "CI" {
		t.Errorf("Workflow = %q, want %q", meta.Workflow, "CI")
	}
	if meta.RunID != "9000000001" {
		t.Errorf("RunID = %q, want %q", meta.RunID, "9000000001")
	}
	if meta.Actor != "monalisa" {
		t.Errorf("Actor = %q, want %q", meta.Actor, "monalisa")
	}
	if meta.JobName != "build" {
		t.Errorf("JobName = %q, want %q", meta.JobName, "build")
	}
}

func TestParseWorkerLog_Failed(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "test", "Worker_failed.log"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	meta := ParseWorkerLog(string(content))
	if meta.Repo != "octocat/hello-world" {
		t.Errorf("Repo = %q, want %q", meta.Repo, "octocat/hello-world")
	}
	if meta.JobName != "test" {
		t.Errorf("JobName = %q, want %q", meta.JobName, "test")
	}
}

func TestParseWorkerLog_Cancelled(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "test", "Worker_cancelled.log"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	meta := ParseWorkerLog(string(content))
	if meta.Repo != "octocat/hello-world" {
		t.Errorf("Repo = %q, want %q", meta.Repo, "octocat/hello-world")
	}
	if meta.Workflow != "Release" {
		t.Errorf("Workflow = %q, want %q", meta.Workflow, "Release")
	}
	if meta.Actor != "monalisa" {
		t.Errorf("Actor = %q, want %q", meta.Actor, "monalisa")
	}
}

func TestParseWorkerLog_Rerun(t *testing.T) {
	// run_attempt > 1 is just another ignored k/v entry; format is identical.
	content, err := os.ReadFile(filepath.Join("..", "..", "test", "Worker_rerun.log"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	meta := ParseWorkerLog(string(content))
	if meta.Repo != "octocat/hello-world" {
		t.Errorf("Repo = %q, want %q", meta.Repo, "octocat/hello-world")
	}
	if meta.JobName != "build-docker-image" {
		t.Errorf("JobName = %q, want %q", meta.JobName, "build-docker-image")
	}
	if meta.RunID != "9000000004" {
		t.Errorf("RunID = %q, want %q", meta.RunID, "9000000004")
	}
}

func TestParseWorkerLog_OldFormat(t *testing.T) {
	// Runner < 2.333.1: metadata is a flat JSON object on a single log line.
	content, err := os.ReadFile(filepath.Join("..", "..", "test", "Worker_old_format.log"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	meta := ParseWorkerLog(string(content))
	if meta.Repo != "octocat/hello-world" {
		t.Errorf("Repo = %q, want %q", meta.Repo, "octocat/hello-world")
	}
	if meta.Workflow != "Legacy Build" {
		t.Errorf("Workflow = %q, want %q", meta.Workflow, "Legacy Build")
	}
	if meta.RunID != "9000000005" {
		t.Errorf("RunID = %q, want %q", meta.RunID, "9000000005")
	}
	if meta.Actor != "monalisa" {
		t.Errorf("Actor = %q, want %q", meta.Actor, "monalisa")
	}
	if meta.JobName != "package" {
		t.Errorf("JobName = %q, want %q", meta.JobName, "package")
	}
}

func TestParseWorkerLog_CancelledEarly(t *testing.T) {
	// Worker process killed before "Job message:" section was written.
	// ParseWorkerLog must return an empty WorkerMeta without panicking.
	content, err := os.ReadFile(filepath.Join("..", "..", "test", "Worker_cancelled_early.log"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	meta := ParseWorkerLog(string(content))
	if meta.Repo != "" || meta.Workflow != "" || meta.Actor != "" || meta.JobName != "" {
		t.Errorf("expected empty WorkerMeta for truncated log, got %+v", meta)
	}
}

// TODO: matrix jobs produce a jobDisplayName like "build / ubuntu-latest" where
// the part after the slash is the matrix dimension value. The Runner log emits
// the same composite name via WRITE LINE, so ParseLine and ParseWorkerLog should
// handle them identically to slash-separated workflow/job names already tested
// (e.g. "build-and-deploy / deploy-in-azure"). Verify with a real matrix Worker
// log when one becomes available.

func TestParseWorkerLog_NoJSON(t *testing.T) {
	meta := ParseWorkerLog("just plain text\nno json here\n")
	if meta.Repo != "" || meta.Workflow != "" || meta.RunID != "" {
		t.Errorf("expected empty WorkerMeta for plain text log, got %+v", meta)
	}
}

func TestParseWorkerLog_MalformedJSON(t *testing.T) {
	// Should not panic; k/v scanner ignores lines it cannot parse.
	meta := ParseWorkerLog("[2024-01-01 00:00:00Z INFO Worker] {not valid json}\n")
	if meta.Repo != "" {
		t.Errorf("expected empty Repo for malformed JSON, got %q", meta.Repo)
	}
}

func TestParseWorkerLog_EmptyRelevantFields(t *testing.T) {
	// Valid JSON but without the fields we care about — should be skipped.
	meta := ParseWorkerLog(`[2024-01-01 00:00:00Z INFO Worker] {"foo":"bar","baz":42}`)
	if meta.Repo != "" || meta.JobName != "" {
		t.Errorf("expected empty WorkerMeta for irrelevant JSON, got %+v", meta)
	}
}

func TestParseWorkerLog_CRLF(t *testing.T) {
	// New format with CRLF line endings.
	content := "[2024-03-15 08:05:10Z INFO Worker] Job message:\r\n {\r\n  \"jobDisplayName\": \"build\",\r\n  \"contextData\": {\r\n    \"github\": {\r\n      \"t\": 2,\r\n      \"d\": [\r\n        {\r\n          \"k\": \"repository\",\r\n          \"v\": \"org/repo\"\r\n        }\r\n      ]\r\n    }\r\n  }\r\n}\r\n"
	meta := ParseWorkerLog(content)
	if meta.Repo != "org/repo" {
		t.Errorf("Repo = %q, want %q", meta.Repo, "org/repo")
	}
}

func TestParseWorkerLog_ExtractsStartAndFinishTime(t *testing.T) {
	content := `
{
  "startTime": "2026-04-01T04:23:24.6296643Z",
  "finishTime": "2026-04-01T04:23:26.9329977Z",
  "jobDisplayName": "validate"
}`
	meta := ParseWorkerLog(content)
	if meta.StartedAt.IsZero() {
		t.Fatal("StartedAt is zero, want parsed timestamp")
	}
	if meta.EndedAt.IsZero() {
		t.Fatal("EndedAt is zero, want parsed timestamp")
	}
	if !meta.EndedAt.After(meta.StartedAt) {
		t.Fatalf("EndedAt = %v, StartedAt = %v, want EndedAt after StartedAt", meta.EndedAt, meta.StartedAt)
	}
}

func TestParseWorkerLog_DoesNotStopBeforeLateTimingFields(t *testing.T) {
	content := `
{
  "jobDisplayName": "validate",
  "contextData": {
    "github": {
      "t": 2,
      "d": [
        {
          "k": "repository",
          "v": "ticklemeozmo/mtg-bulk-import"
        },
        {
          "k": "workflow",
          "v": "Validate"
        },
        {
          "k": "run_id",
          "v": "12345"
        },
        {
          "k": "actor",
          "v": "jnovack"
        }
      ]
    }
  }
}
... lots of log lines ...
{
  "startTime": "2026-04-01T04:23:24.6296643Z",
  "finishTime": "2026-04-01T04:23:26.9329977Z"
}`
	meta := ParseWorkerLog(content)
	if meta.Repo == "" || meta.Workflow == "" || meta.RunID == "" || meta.Actor == "" || meta.JobName == "" {
		t.Fatalf("expected full metadata, got %+v", meta)
	}
	if meta.StartedAt.IsZero() || meta.EndedAt.IsZero() {
		t.Fatalf("expected timing fields, got StartedAt=%v EndedAt=%v", meta.StartedAt, meta.EndedAt)
	}
	if !meta.EndedAt.After(meta.StartedAt) {
		t.Fatalf("EndedAt = %v, StartedAt = %v, want EndedAt after StartedAt", meta.EndedAt, meta.StartedAt)
	}
}

func TestParseLine_TypicalLogFile(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "test", "Runner_typical.log"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	wantSequence := []EventKind{
		EventOnline,
		EventJobStarted,
		EventJobCompleted,
		EventOnline,
		EventJobStarted,
		EventJobCompleted,
		EventOnline,
		EventJobStarted,
		EventJobCompleted,
		EventOnline,
	}

	var got []EventKind
	for _, line := range splitLines(string(content)) {
		if ev, ok := ParseLine(line); ok {
			got = append(got, ev.Kind)
		}
	}

	if len(got) != len(wantSequence) {
		t.Fatalf("parsed %d events, want %d; events: %v", len(got), len(wantSequence), got)
	}
	for i := range got {
		if got[i] != wantSequence[i] {
			t.Errorf("event[%d] = %v, want %v", i, got[i], wantSequence[i])
		}
	}
}

func TestParseLine_OldFormatLogFile(t *testing.T) {
	// Runner < 2.333.1: no "WRITE LINE:" prefix; component names differ (Runner.Listener, JobDispatcher).
	content, err := os.ReadFile(filepath.Join("..", "..", "test", "Runner_old_format.log"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	wantSequence := []EventKind{
		EventOnline,
		EventJobStarted,
		EventJobCompleted,
		EventOnline,
	}
	var got []EventKind
	for _, line := range splitLines(string(content)) {
		if ev, ok := ParseLine(line); ok {
			got = append(got, ev.Kind)
		}
	}
	if len(got) != len(wantSequence) {
		t.Fatalf("parsed %d events, want %d; events: %v", len(got), len(wantSequence), got)
	}
	for i := range got {
		if got[i] != wantSequence[i] {
			t.Errorf("event[%d] = %v, want %v", i, got[i], wantSequence[i])
		}
	}
}

func TestParseLine_WindowsLogFile(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "test", "Runner_windows.log"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var eventCount int
	for _, line := range splitLines(string(content)) {
		if _, ok := ParseLine(line); ok {
			eventCount++
		}
	}

	if eventCount == 0 {
		t.Error("parsed 0 events from Windows CRLF log; expected same events as typical log")
	}
}

// splitLines splits content on newlines without removing \r so individual
// ParseLine calls can test CRLF stripping behaviour.
func splitLines(s string) []string {
	return splitOn(s, '\n')
}

func splitOn(s string, sep byte) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
