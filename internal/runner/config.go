package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

	return &c, nil
}

// DiagDir returns the absolute path to the runner's _diag directory.
func (c *Config) DiagDir(runnerDir string) string {
	return filepath.Join(runnerDir, "_diag")
}

// OS returns the operating system name for use as a metric label.
func OS() string {
	return runtime.GOOS
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
