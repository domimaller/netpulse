package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultIsValid(t *testing.T) {
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.LatencyProbes.ProbeTimeout != 900*time.Millisecond || len(c.LatencyProbes.InternetTargets) != 3 {
		t.Fatalf("unexpected defaults: %+v", c.LatencyProbes)
	}
}

func TestOverrideKeepsOtherDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("latency_probes:\n  internet_targets: [1.0.0.1]\nspeed_estimator:\n  daily_data_budget_mb: 50\n"), 0o644)
	c, used, err := Load(p, true)
	if err != nil || used != p {
		t.Fatal(err)
	}
	if len(c.LatencyProbes.InternetTargets) != 1 || c.SpeedEstimator.DailyDataBudgetMB != 50 {
		t.Fatalf("override not applied: %+v", c)
	}
	if c.LatencyProbes.ProbeTimeout != 900*time.Millisecond || c.WebServer.ListenAddress != "127.0.0.1:7171" {
		t.Fatal("defaults lost")
	}
}

func TestUnknownKeyRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("latency_probes:\n  internet_target: [1.0.0.1]\n"), 0o644)
	if _, _, err := Load(p, true); err == nil {
		t.Fatal("expected error for typo'd key")
	}
}

func TestEmptyFileAndMissingDefault(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, nil, 0o644)
	if _, _, err := Load(p, true); err != nil {
		t.Fatal(err)
	}
	if _, used, err := Load(filepath.Join(t.TempDir(), "nope.yaml"), false); err != nil || used != "" {
		t.Fatal("missing default path should fall back to defaults")
	}
}

func TestRepoExampleMatchesEmbedded(t *testing.T) {
	b, err := os.ReadFile("../../netpulse.example.yaml")
	if err != nil {
		t.Skip("example not present")
	}
	if string(b) != string(DefaultYAML) {
		t.Fatal("netpulse.example.yaml is out of sync with internal/config/default.yaml (run: make example-config)")
	}
}
