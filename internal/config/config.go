// Package config holds every netpulse setting. The commented default.yaml
// next to this file is the single source of truth for both the default
// values and their documentation (`netpulse -print-default-config`).
package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed default.yaml
var DefaultYAML []byte

type Config struct {
	WebServer      WebServer      `yaml:"web_server" json:"web_server"`
	LatencyProbes  LatencyProbes  `yaml:"latency_probes" json:"latency_probes"`
	DNSProbe       DNSProbe       `yaml:"dns_probe" json:"dns_probe"`
	QualityRules   QualityRules   `yaml:"quality_rules" json:"quality_rules"`
	History        History        `yaml:"history" json:"history"`
	SpeedEstimator SpeedEstimator `yaml:"speed_estimator" json:"speed_estimator"`
	WebUI          WebUI          `yaml:"web_ui" json:"web_ui"`
}

type WebServer struct {
	ListenAddress string `yaml:"listen_address" json:"listen_address"`
}

type LatencyProbes struct {
	InternetTargets      []string      `yaml:"internet_targets" json:"internet_targets"`
	ProbeTimeout         time.Duration `yaml:"probe_timeout" json:"probe_timeout"`
	ProbeMethod          string        `yaml:"probe_method" json:"probe_method"`
	TCPProbePort         int           `yaml:"tcp_probe_port" json:"tcp_probe_port"`
	RouterProbeEnabled   bool          `yaml:"router_probe_enabled" json:"router_probe_enabled"`
	RouteRefreshInterval time.Duration `yaml:"route_refresh_interval" json:"route_refresh_interval"`
}

type DNSProbe struct {
	Enabled        bool   `yaml:"enabled" json:"enabled"`
	LookupHostname string `yaml:"lookup_hostname" json:"lookup_hostname"`
}

type QualityRules struct {
	DegradedBelowScore        float64       `yaml:"degraded_below_score" json:"degraded_below_score"`
	LatencySpikeMultiplier    float64       `yaml:"latency_spike_multiplier" json:"latency_spike_multiplier"`
	LatencySpikeMinIncreaseMs float64       `yaml:"latency_spike_min_increase_ms" json:"latency_spike_min_increase_ms"`
	BaselineWindow            time.Duration `yaml:"baseline_window" json:"baseline_window"`
}

type History struct {
	KeepInMemory time.Duration `yaml:"keep_in_memory" json:"keep_in_memory"`
	RecordToFile string        `yaml:"record_to_file" json:"record_to_file"`
}

type SpeedEstimator struct {
	AutomaticProbesEnabled bool   `yaml:"automatic_probes_enabled" json:"automatic_probes_enabled"`
	DownloadURL            string `yaml:"download_url" json:"download_url"`
	UploadURL              string `yaml:"upload_url" json:"upload_url"`

	IdleTrafficThresholdKbps         float64       `yaml:"idle_traffic_threshold_kbps" json:"idle_traffic_threshold_kbps"`
	IdleRequiredFor                  time.Duration `yaml:"idle_required_for" json:"idle_required_for"`
	MaxLatencyAboveBaselineToStartMs float64       `yaml:"max_latency_above_baseline_to_start_ms" json:"max_latency_above_baseline_to_start_ms"`
	FirstProbeDelay                  time.Duration `yaml:"first_probe_delay" json:"first_probe_delay"`
	DailyDataBudgetMB                float64       `yaml:"daily_data_budget_mb" json:"daily_data_budget_mb"`

	TargetProbeDuration time.Duration `yaml:"target_probe_duration" json:"target_probe_duration"`
	MinProbeSizeMB      float64       `yaml:"min_probe_size_mb" json:"min_probe_size_mb"`
	MaxProbeSizeMB      float64       `yaml:"max_probe_size_mb" json:"max_probe_size_mb"`

	AbortWhenLatencyRisesMs float64 `yaml:"abort_when_latency_rises_ms" json:"abort_when_latency_rises_ms"`

	MinProbeInterval           time.Duration `yaml:"min_probe_interval" json:"min_probe_interval"`
	MaxProbeInterval           time.Duration `yaml:"max_probe_interval" json:"max_probe_interval"`
	IntervalGrowthFactor       float64       `yaml:"interval_growth_factor" json:"interval_growth_factor"`
	StableResultTolerancePct   float64       `yaml:"stable_result_tolerance_pct" json:"stable_result_tolerance_pct"`
	UploadProbeEveryNthSession int           `yaml:"upload_probe_every_nth_session" json:"upload_probe_every_nth_session"`
	PassivePeakWindow          time.Duration `yaml:"passive_peak_window" json:"passive_peak_window"`

	FullTestParallelStreams      int           `yaml:"full_test_parallel_streams" json:"full_test_parallel_streams"`
	FullTestDurationPerDirection time.Duration `yaml:"full_test_duration_per_direction" json:"full_test_duration_per_direction"`
}

type WebUI struct {
	DefaultTimeRange string `yaml:"default_time_range" json:"default_time_range"`
}

// Default returns the built-in defaults (parsed from default.yaml).
func Default() Config {
	var c Config
	if err := decode(DefaultYAML, &c); err != nil {
		panic("embedded default.yaml is invalid: " + err.Error())
	}
	return c
}

func decode(b []byte, c *Config) error {
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	// An empty file decodes as io.EOF: that just means "no overrides".
	if err := d.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// DefaultPath is ~/.config/netpulse/config.yaml (or $XDG_CONFIG_HOME/netpulse/config.yaml).
func DefaultPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "netpulse", "config.yaml")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "netpulse", "config.yaml")
}

// Load reads path on top of the defaults. If explicit is false and the file
// does not exist, the defaults are returned. It returns the path actually used ("" = defaults only).
func Load(path string, explicit bool) (Config, string, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return c, "", nil
		}
		return c, "", err
	}
	if err := decode(b, &c); err != nil {
		return c, path, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return c, path, fmt.Errorf("%s: %w", path, err)
	}
	return c, path, nil
}

// ExpandHome turns a leading ~ into the home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, strings.TrimPrefix(p, "~"))
	}
	return p
}

// Validate reports the first nonsensical setting, naming its YAML key.
func (c Config) Validate() error {
	var errs []string
	bad := func(key, why string) { errs = append(errs, key+": "+why) }

	if _, _, err := net.SplitHostPort(c.WebServer.ListenAddress); err != nil {
		bad("web_server.listen_address", err.Error())
	}
	lp := c.LatencyProbes
	if len(lp.InternetTargets) == 0 {
		bad("latency_probes.internet_targets", "need at least one target")
	}
	for _, t := range lp.InternetTargets {
		if ip := net.ParseIP(t); ip == nil || ip.To4() == nil {
			bad("latency_probes.internet_targets", fmt.Sprintf("%q is not an IPv4 address", t))
		}
	}
	if lp.ProbeTimeout <= 0 || lp.ProbeTimeout >= time.Second {
		bad("latency_probes.probe_timeout", "must be between 0 and 1s")
	}
	switch lp.ProbeMethod {
	case "auto", "icmp", "tcp":
	default:
		bad("latency_probes.probe_method", "must be auto, icmp or tcp")
	}
	if lp.TCPProbePort <= 0 || lp.TCPProbePort > 65535 {
		bad("latency_probes.tcp_probe_port", "must be 1-65535")
	}
	if lp.RouteRefreshInterval < time.Second {
		bad("latency_probes.route_refresh_interval", "must be at least 1s")
	}
	if c.DNSProbe.Enabled && c.DNSProbe.LookupHostname == "" {
		bad("dns_probe.lookup_hostname", "required when dns_probe.enabled is true")
	}
	q := c.QualityRules
	if q.DegradedBelowScore < 0 || q.DegradedBelowScore > 100 {
		bad("quality_rules.degraded_below_score", "must be 0-100")
	}
	if q.LatencySpikeMultiplier < 1 {
		bad("quality_rules.latency_spike_multiplier", "must be >= 1")
	}
	if q.BaselineWindow < 10*time.Second {
		bad("quality_rules.baseline_window", "must be at least 10s")
	}
	if c.History.KeepInMemory < time.Minute {
		bad("history.keep_in_memory", "must be at least 1m")
	}
	s := c.SpeedEstimator
	if !strings.Contains(s.DownloadURL, "%d") {
		bad("speed_estimator.download_url", "must contain %d for the byte count")
	}
	if s.UploadURL == "" {
		bad("speed_estimator.upload_url", "required")
	}
	if s.IdleRequiredFor < time.Second {
		bad("speed_estimator.idle_required_for", "must be at least 1s")
	}
	if s.TargetProbeDuration < 50*time.Millisecond {
		bad("speed_estimator.target_probe_duration", "must be at least 50ms")
	}
	if s.MinProbeSizeMB <= 0 || s.MaxProbeSizeMB < s.MinProbeSizeMB {
		bad("speed_estimator.min_probe_size_mb/max_probe_size_mb", "need 0 < min <= max")
	}
	if s.MinProbeInterval < 10*time.Second || s.MaxProbeInterval < s.MinProbeInterval {
		bad("speed_estimator.min_probe_interval/max_probe_interval", "need 10s <= min <= max")
	}
	if s.IntervalGrowthFactor < 1 {
		bad("speed_estimator.interval_growth_factor", "must be >= 1")
	}
	if s.UploadProbeEveryNthSession < 0 {
		bad("speed_estimator.upload_probe_every_nth_session", "must be >= 0 (0 disables automatic upload probes)")
	}
	if s.FullTestParallelStreams < 1 || s.FullTestParallelStreams > 16 {
		bad("speed_estimator.full_test_parallel_streams", "must be 1-16")
	}
	if s.FullTestDurationPerDirection < time.Second || s.FullTestDurationPerDirection > time.Minute {
		bad("speed_estimator.full_test_duration_per_direction", "must be 1s-1m")
	}
	switch c.WebUI.DefaultTimeRange {
	case "1m", "5m", "15m", "1h", "6h", "24h":
	default:
		bad("web_ui.default_time_range", "must be one of 1m, 5m, 15m, 1h, 6h, 24h")
	}
	if len(errs) > 0 {
		return errors.New("invalid config:\n  " + strings.Join(errs, "\n  "))
	}
	return nil
}
