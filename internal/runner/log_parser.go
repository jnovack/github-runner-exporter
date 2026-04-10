package runner

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// EventKind identifies what type of runner event a log line represents.
type EventKind int

const (
	// EventOnline is emitted when the runner logs "Listening for Jobs".
	EventOnline EventKind = iota
	// EventJobStarted is emitted when the runner logs "Running job: <name>".
	EventJobStarted
	// EventJobCompleted is emitted when the runner logs "Job <name> completed with result: <result>".
	EventJobCompleted
)

// Event is a parsed runner log entry that represents a meaningful state change.
type Event struct {
	Kind      EventKind
	Timestamp time.Time
	JobName   string // EventJobStarted, EventJobCompleted
	Result    string // EventJobCompleted: "succeeded", "failed", "canceled"
}

// WorkerMeta holds job metadata extracted from a Worker_*.log file.
type WorkerMeta struct {
	Repo      string `json:"repository"`
	Workflow  string `json:"workflow_name"`
	RunID     string `json:"run_id"`
	Actor     string `json:"actor"`
	JobName   string `json:"job_name"`
	StartedAt time.Time
	EndedAt   time.Time
}

// logLineRe matches the standard runner log format:
// [YYYY-MM-DD HH:MM:SSZ LEVEL Component] message
var logLineRe = regexp.MustCompile(
	`^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})Z[^\]]*\]\s+(.+)$`,
)

const logTimeLayout = "2006-01-02 15:04:05"

// ParseLine parses a single Runner_*.log line into an Event.
// Returns (event, true) on match, (zero, false) if the line is not actionable.
func ParseLine(line string) (Event, bool) {
	// Strip Windows CRLF endings before matching.
	line = strings.TrimRight(line, "\r")

	m := logLineRe.FindStringSubmatch(line)
	if m == nil {
		return Event{}, false
	}

	ts, err := time.Parse(logTimeLayout, m[1])
	if err != nil {
		return Event{}, false
	}
	ts = ts.UTC()

	msg := m[2]

	// Runner >= 2.333 routes console output through the Terminal component with a
	// "WRITE LINE: DATETIME: " prefix. Strip it so the switch below works for
	// both old and new runner versions.
	msg = normalizeWriteLineMessage(msg)

	switch {
	case msg == "Listening for Jobs":
		return Event{Kind: EventOnline, Timestamp: ts}, true

	case strings.HasPrefix(msg, "Running job: "):
		jobName := strings.TrimPrefix(msg, "Running job: ")
		return Event{Kind: EventJobStarted, Timestamp: ts, JobName: jobName}, true

	case strings.HasPrefix(msg, "Job request ") && strings.Contains(msg, " received."):
		// Fallback for cases where "Running job: ..." is missed due log tail timing.
		// Example:
		// "Job request 0 for plan <plan> job <id> received."
		return Event{Kind: EventJobStarted, Timestamp: ts}, true

	case strings.HasPrefix(msg, "Job ") && strings.Contains(msg, " completed with result: "):
		// "Job <name> completed with result: Succeeded"
		rest := strings.TrimPrefix(msg, "Job ")
		idx := strings.LastIndex(rest, " completed with result: ")
		if idx < 0 {
			return Event{}, false
		}
		jobName := rest[:idx]
		result := strings.ToLower(rest[idx+len(" completed with result: "):])
		// Normalize British "cancelled" to US "canceled" — GitHub Actions runners
		// emit "Canceled" (one l) but some older runners/fixtures use "Cancelled".
		if result == "cancelled" {
			result = "canceled"
		}
		return Event{Kind: EventJobCompleted, Timestamp: ts, JobName: jobName, Result: result}, true
	}

	return Event{}, false
}

// normalizeWriteLineMessage unwraps Terminal "WRITE LINE:" log payloads.
// Newer runners often prepend a console timestamp inside the message (for
// example: "2024-03-15 08:00:02Z: Listening for Jobs"), but some lines may not.
func normalizeWriteLineMessage(msg string) string {
	const writeLine = "WRITE LINE: "
	if !strings.HasPrefix(msg, writeLine) {
		return msg
	}
	rest := strings.TrimSpace(msg[len(writeLine):])
	parts := strings.SplitN(rest, ": ", 2)
	if len(parts) != 2 {
		return rest
	}
	if isRunnerConsoleTimestamp(parts[0]) {
		return parts[1]
	}
	return rest
}

func isRunnerConsoleTimestamp(s string) bool {
	layouts := []string{
		"2006-01-02 15:04:05Z",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02T15:04:05.000Z07:00",
	}
	for _, layout := range layouts {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
}

// ParseWorkerLog scans all lines in a Worker_*.log file content for job metadata.
// It handles two formats:
//
//   - Old (runner < 2.333): a single-line flat JSON object with repository, workflow_name,
//     run_id, actor, and job_name fields.
//
//   - New (runner >= 2.333): a multi-line JSON job message where jobDisplayName holds the
//     job name, and github context fields (repository, run_id, actor, workflow) appear as
//     {"k": "key", "v": "value"} pairs.
func ParseWorkerLog(content string) WorkerMeta {
	var meta WorkerMeta
	var pendingKey string

	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)

		// Old format: single-line flat JSON with known fields.
		if idx := strings.Index(line, "{"); idx >= 0 {
			var m WorkerMeta
			if err := json.Unmarshal([]byte(line[idx:]), &m); err == nil {
				if m.Repo != "" || m.JobName != "" {
					if m.Repo != "" {
						meta.Repo = m.Repo
					}
					if m.Workflow != "" {
						meta.Workflow = m.Workflow
					}
					if m.RunID != "" {
						meta.RunID = m.RunID
					}
					if m.Actor != "" {
						meta.Actor = m.Actor
					}
					if m.JobName != "" {
						meta.JobName = m.JobName
					}
				}
			}
		}

		// New format: "jobDisplayName": "value"
		if meta.JobName == "" {
			if v := extractJSONStringField(trimmed, "jobDisplayName"); v != "" {
				meta.JobName = v
			}
		}
		if meta.StartedAt.IsZero() {
			if v := extractJSONStringField(trimmed, "startTime"); v != "" {
				if ts, ok := parseWorkerTimestamp(v); ok {
					meta.StartedAt = ts
				}
			}
		}
		if meta.EndedAt.IsZero() {
			if v := extractJSONStringField(trimmed, "finishTime"); v != "" {
				if ts, ok := parseWorkerTimestamp(v); ok {
					meta.EndedAt = ts
				}
			}
		}

		// New format: {"k": "key"} on one line, {"v": "value"} on the next.
		if trimmed == "{" {
			pendingKey = ""
		} else if k := extractJSONStringField(trimmed, "k"); k != "" {
			pendingKey = k
		} else if pendingKey != "" {
			if v := extractJSONStringField(trimmed, "v"); v != "" {
				switch pendingKey {
				case "repository":
					if meta.Repo == "" {
						meta.Repo = v
					}
				case "run_id":
					if meta.RunID == "" {
						meta.RunID = v
					}
				case "actor":
					if meta.Actor == "" {
						meta.Actor = v
					}
				case "workflow":
					if meta.Workflow == "" {
						meta.Workflow = v
					}
				}
				pendingKey = ""
			}
		}

		if meta.Repo != "" && meta.Workflow != "" && meta.RunID != "" && meta.Actor != "" && meta.JobName != "" &&
			!meta.StartedAt.IsZero() && !meta.EndedAt.IsZero() {
			break
		}
	}

	return meta
}

func parseWorkerTimestamp(s string) (time.Time, bool) {
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return ts.UTC(), true
}

// extractJSONStringField returns the string value for the given key in a JSON line fragment.
// Handles both `"key": "value"` and `"key": "value",` forms.
func extractJSONStringField(line, key string) string {
	prefix := `"` + key + `": "`
	idx := strings.Index(line, prefix)
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(prefix):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}
