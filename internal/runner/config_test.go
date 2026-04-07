package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("..", "..", "test", "runner_config_valid.json"))
	if err != nil {
		t.Fatalf("read valid fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".runner"), data, 0600); err != nil {
		t.Fatalf("write .runner: %v", err)
	}
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig returned unexpected error: %v", err)
	}
	if cfg.AgentName != "runner-prod-01" {
		t.Errorf("AgentName = %q, want %q", cfg.AgentName, "runner-prod-01")
	}
	if cfg.AgentID != 42 {
		t.Errorf("AgentID = %d, want 42", cfg.AgentID)
	}
	if cfg.PoolName != "Default" {
		t.Errorf("PoolName = %q, want %q", cfg.PoolName, "Default")
	}
	if cfg.WorkFolder != "_work" {
		t.Errorf("WorkFolder = %q, want %q", cfg.WorkFolder, "_work")
	}
}

func TestLoadConfig_Minimal(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("..", "..", "test", "runner_config_minimal.json"))
	if err != nil {
		t.Fatalf("read minimal fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".runner"), data, 0600); err != nil {
		t.Fatalf("write .runner: %v", err)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig returned unexpected error: %v", err)
	}
	if cfg.AgentName != "minimal-runner" {
		t.Errorf("AgentName = %q, want %q", cfg.AgentName, "minimal-runner")
	}
	// WorkFolder should default to "_work" when not specified.
	if cfg.WorkFolder != "_work" {
		t.Errorf("WorkFolder = %q, want default %q", cfg.WorkFolder, "_work")
	}
}

func TestLoadConfig_UTF8BOM(t *testing.T) {
	dir := t.TempDir()
	bom := []byte("\xef\xbb\xbf")
	data := append(bom, []byte(`{"AgentId":1,"AgentName":"bom-runner","PoolId":1,"PoolName":"Default","WorkFolder":"_work"}`)...)
	if err := os.WriteFile(filepath.Join(dir, ".runner"), data, 0600); err != nil {
		t.Fatalf("write .runner: %v", err)
	}
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig returned unexpected error for BOM-prefixed file: %v", err)
	}
	if cfg.AgentName != "bom-runner" {
		t.Errorf("AgentName = %q, want %q", cfg.AgentName, "bom-runner")
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for missing .runner file, got nil")
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte("{not valid json"), 0600); err != nil {
		t.Fatalf("write .runner: %v", err)
	}
	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestLoadConfig_EmptyAgentName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".runner"), []byte(`{"AgentId":1,"AgentName":""}`), 0600); err != nil {
		t.Fatalf("write .runner: %v", err)
	}
	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("expected error for empty AgentName, got nil")
	}
}

func TestDiagDir(t *testing.T) {
	cfg := &Config{}
	got := cfg.DiagDir("/home/runner/actions-runner")
	want := filepath.Join("/home/runner/actions-runner", "_diag")
	if got != want {
		t.Errorf("DiagDir = %q, want %q", got, want)
	}
}

func TestDefaultRunnerDir_NotEmpty(t *testing.T) {
	dir := DefaultRunnerDir()
	if dir == "" {
		t.Error("DefaultRunnerDir returned empty string")
	}
}

func TestOS_NotEmpty(t *testing.T) {
	if OS() == "" {
		t.Error("OS() returned empty string")
	}
}
