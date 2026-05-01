package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Config holds the parsed contents of the runner's .runner configuration file.
type Config struct {
	AgentID     int    `json:"AgentId"`
	AgentName   string `json:"AgentName"`
	PoolID      int    `json:"PoolId"`
	PoolName    string `json:"PoolName"`
	ServerURL   string `json:"ServerUrl"`
	GitHubURL   string `json:"GitHubUrl"`
	WorkFolder  string `json:"WorkFolder"`
	IsEphemeral bool   `json:"IsEphemeral"`

	// AgentVersion is the runner agent version (e.g. "2.334.0"), detected at
	// load time from the "bin" symlink in the runner directory. Empty string if
	// not detectable.
	AgentVersion string `json:"-"`
}

// LoadConfig reads and parses the .runner file in the given runner directory.
func LoadConfig(runnerDir string) (*Config, error) {
	path := filepath.Join(runnerDir, ".runner")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read .runner config: %w", err)
	}

	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse .runner config: %w", err)
	}

	if c.AgentName == "" {
		return nil, fmt.Errorf("parse .runner config: AgentName is empty")
	}

	if c.WorkFolder == "" {
		c.WorkFolder = "_work"
	}

	if link, err := os.Readlink(filepath.Join(runnerDir, "bin")); err == nil {
		if v, ok := strings.CutPrefix(filepath.Base(link), "bin."); ok {
			c.AgentVersion = v
		}
	}

	if c.AgentVersion == "" {
		c.AgentVersion = detectVersionFromDiagLogs(filepath.Join(runnerDir, "_diag"))
	}

	return &c, nil
}

// diagBinVersionRe matches "bin.2.334.0" anywhere in a Runner diag log line.
var diagBinVersionRe = regexp.MustCompile(`\bbin\.(\d+\.\d+\.\d+)\b`)

// detectVersionFromDiagLogs scans the most recently created Runner_*.log in
// diagDir for the HostContext "Well known directory 'Bin'" startup line and
// extracts the runner agent version from it. Returns "" if not found.
func detectVersionFromDiagLogs(diagDir string) string {
	entries, err := filepath.Glob(filepath.Join(diagDir, "Runner_*.log"))
	if err != nil || len(entries) == 0 {
		return ""
	}
	// filepath.Glob returns alphabetical order; Runner logs use timestamp-based
	// names (Runner_YYYYMMDD-HHMMSS-utc_PID.log), so the last entry is newest.
	f, err := os.Open(entries[len(entries)-1])
	if err != nil {
		return ""
	}
	defer f.Close()

	// The version line appears in the first few hundred lines of every log.
	scanner := bufio.NewScanner(io.LimitReader(f, 64*1024))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Bin") {
			if m := diagBinVersionRe.FindStringSubmatch(line); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

// DiagDir returns the absolute path to the runner's _diag directory.
func (c *Config) DiagDir(runnerDir string) string {
	return filepath.Join(runnerDir, "_diag")
}

// OS returns the operating system name for use as a metric label.
func OS() string {
	return runtime.GOOS
}

// Arch returns the CPU architecture name for use as a metric label.
func Arch() string {
	return runtime.GOARCH
}

// DefaultRunnerDir returns the platform-appropriate default runner installation directory.
func DefaultRunnerDir() string {
	switch runtime.GOOS {
	case "windows":
		return `C:\actions-runner`
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "actions-runner"
		}
		return filepath.Join(home, "actions-runner")
	default: // linux and others
		return "/actions-runner"
	}
}
