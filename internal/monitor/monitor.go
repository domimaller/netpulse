package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/domimaller/netpulse/internal/config"
	"github.com/domimaller/netpulse/internal/probe"
	"github.com/domimaller/netpulse/internal/sysnet"
)

// Config for the per-second monitor (documented in internal/config/default.yaml).
type Config struct {
	Probes  config.LatencyProbes
	DNS     config.DNSProbe
	Quality config.QualityRules
	History config.History
}

// ProbeAccounting lets the speed estimator tell the monitor how many bytes
// it moved, so they can be subtracted from interface counters (we want to
// show *your* traffic, and the estimator needs to know if *you* are busy).
type ProbeAccounting interface {
	TakeBytes() (rx, tx uint64, active bool)
}

// Info is static-ish environment information.
type Info struct {
	Mode    string       `json:"mode"`
	Targets []string     `json:"targets"`
	Route   sysnet.Route `json:"route"`
	DNSName string       `json:"dns_name"`
	Started int64        `json:"started"`
}

// Monitor runs the probes once a second and keeps a ring of Samples.
type Monitor struct {
	cfg    Config
	pinger probe.Pinger
	dns    *probe.DNS
	acct   ProbeAccounting

	mu      sync.RWMutex
	ring    []Sample
	head    int
	n       int
	route   sysnet.Route
	lastRTT map[string]float64
	lastCtr sysnet.Counters
	lastAt  time.Time
	started time.Time

	netGen atomic.Uint64 // bumps whenever the default route changes

	subMu sync.Mutex
	subs  map[chan Sample]struct{}

	rec *os.File
}

func New(cfg Config) (*Monitor, error) {
	pinger, err := probe.New(cfg.Probes.ProbeMethod, cfg.Probes.TCPProbePort)
	if err != nil {
		return nil, err
	}
	m := &Monitor{
		cfg:     cfg,
		pinger:  pinger,
		dns:     probe.NewDNS(),
		ring:    make([]Sample, int(cfg.History.KeepInMemory/time.Second)),
		lastRTT: map[string]float64{},
		subs:    map[chan Sample]struct{}{},
		started: time.Now(),
	}
	if p := config.ExpandHome(cfg.History.RecordToFile); p != "" {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, fmt.Errorf("history.record_to_file: %w", err)
		}
		m.rec = f
	}
	return m, nil
}

func (m *Monitor) SetAccounting(a ProbeAccounting)   { m.acct = a }
func (m *Monitor) QualityRules() config.QualityRules { return m.cfg.Quality }
func (m *Monitor) Pinger() probe.Pinger              { return m.pinger }
func (m *Monitor) NetworkGeneration() uint64         { return m.netGen.Load() }

func (m *Monitor) Info() Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	dns := ""
	if m.cfg.DNS.Enabled {
		dns = m.cfg.DNS.LookupHostname
	}
	return Info{Mode: m.pinger.Mode(), Targets: m.cfg.Probes.InternetTargets, Route: m.route, DNSName: dns, Started: m.started.UnixMilli()}
}

// Subscribe returns a channel receiving every new sample.
func (m *Monitor) Subscribe() (<-chan Sample, func()) {
	ch := make(chan Sample, 16)
	m.subMu.Lock()
	m.subs[ch] = struct{}{}
	m.subMu.Unlock()
	return ch, func() {
		m.subMu.Lock()
		delete(m.subs, ch)
		m.subMu.Unlock()
	}
}

// Last returns up to n most recent samples, oldest first.
func (m *Monitor) Last(n int) []Sample {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if n > m.n {
		n = m.n
	}
	out := make([]Sample, n)
	start := (m.head - n + len(m.ring)) % len(m.ring)
	for i := 0; i < n; i++ {
		out[i] = m.ring[(start+i)%len(m.ring)]
	}
	return out
}

// Since returns samples with T > sinceMs, oldest first.
func (m *Monitor) Since(sinceMs int64, max int) []Sample {
	all := m.Last(max)
	i := 0
	for i < len(all) && all[i].T <= sinceMs {
		i++
	}
	return all[i:]
}

// BaselineRTT is a robust "unloaded" latency: 10th percentile over
// quality_rules.baseline_window. Used to detect queueing and congestion.
func (m *Monitor) BaselineRTT() (float64, bool) {
	var xs []float64
	for _, s := range m.Last(int(m.cfg.Quality.BaselineWindow / time.Second)) {
		if s.RTTMs != nil && !s.SpeedProbe {
			xs = append(xs, *s.RTTMs)
		}
	}
	if len(xs) < 5 {
		return 0, false
	}
	return percentile(xs, 10), true
}

// Run blocks until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	m.refreshRoute()
	routeTick := time.NewTicker(m.cfg.Probes.RouteRefreshInterval)
	defer routeTick.Stop()

	// Align to wall-clock second boundaries so samples line up.
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second)))
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	m.collect(ctx, time.Now().Truncate(time.Second))
	for {
		select {
		case <-ctx.Done():
			if m.rec != nil {
				m.rec.Close()
			}
			return
		case <-routeTick.C:
			m.refreshRoute()
		case t := <-tick.C:
			m.collect(ctx, t.Truncate(time.Second))
		}
	}
}

func (m *Monitor) refreshRoute() {
	r, err := sysnet.DefaultRoute()
	if err != nil {
		return
	}
	m.mu.Lock()
	changed := r != m.route
	m.route = r
	m.mu.Unlock()
	if changed {
		m.netGen.Add(1)
		log.Printf("default route: gateway=%s interface=%s", r.Gateway, r.Interface)
	}
}

type result struct {
	host string
	rtt  time.Duration
	err  error
}

func (m *Monitor) collect(parent context.Context, sec time.Time) {
	ctx, cancel := context.WithTimeout(parent, m.cfg.Probes.ProbeTimeout)
	defer cancel()

	m.mu.RLock()
	route := m.route
	m.mu.RUnlock()

	var wg sync.WaitGroup
	results := make([]result, len(m.cfg.Probes.InternetTargets))
	for i, h := range m.cfg.Probes.InternetTargets {
		wg.Add(1)
		go func(i int, h string) {
			defer wg.Done()
			rtt, err := m.pinger.Ping(ctx, h)
			results[i] = result{h, rtt, err}
		}(i, h)
	}
	var gwRTT *float64
	if ip := net.ParseIP(route.Gateway); m.cfg.Probes.RouterProbeEnabled && ip != nil && ip.To4() != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rtt, err := m.pinger.Ping(ctx, route.Gateway); err == nil {
				gwRTT = msPtr(rtt)
			}
		}()
	}
	var dnsMs *float64
	if m.cfg.DNS.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if d, err := m.dns.Lookup(ctx, m.cfg.DNS.LookupHostname); err == nil {
				dnsMs = msPtr(d)
			}
		}()
	}
	wg.Wait()

	s := Sample{T: sec.UnixMilli(), TargetRTT: map[string]*float64{}, GatewayMs: gwRTT, DNSMs: dnsMs}
	var ok []float64
	var jit []float64
	lost := 0
	m.mu.Lock()
	for _, r := range results {
		if r.err != nil {
			lost++
			s.TargetRTT[r.host] = nil
			continue
		}
		v := float64(r.rtt.Microseconds()) / 1000
		s.TargetRTT[r.host] = f64(math.Round(v*100) / 100)
		ok = append(ok, v)
		if prev, seen := m.lastRTT[r.host]; seen {
			jit = append(jit, math.Abs(v-prev))
		}
		m.lastRTT[r.host] = v
	}
	m.mu.Unlock()
	if len(results) > 0 {
		s.LossPct = round2(100 * float64(lost) / float64(len(results)))
	}
	if len(ok) > 0 {
		s.RTTMs = f64(round2(median(ok)))
	}
	if len(jit) > 0 {
		s.JitterMs = f64(round2(mean(jit)))
	}

	// Throughput from interface counters, minus our own probe traffic.
	now := time.Now()
	if route.Interface != "" {
		if c, err := sysnet.ReadCounters(route.Interface); err == nil {
			var prx, ptx uint64
			if m.acct != nil {
				prx, ptx, s.SpeedProbe = m.acct.TakeBytes()
			}
			m.mu.Lock()
			if !m.lastAt.IsZero() && c.RxBytes >= m.lastCtr.RxBytes && c.TxBytes >= m.lastCtr.TxBytes {
				dt := now.Sub(m.lastAt).Seconds()
				drx := float64(c.RxBytes-m.lastCtr.RxBytes) - float64(prx)
				dtx := float64(c.TxBytes-m.lastCtr.TxBytes) - float64(ptx)
				s.RxBps = math.Round(math.Max(0, drx) * 8 / dt)
				s.TxBps = math.Round(math.Max(0, dtx) * 8 / dt)
			}
			m.lastCtr, m.lastAt = c, now
			m.mu.Unlock()
		}
	}

	m.classify(&s)

	m.mu.Lock()
	m.ring[m.head] = s
	m.head = (m.head + 1) % len(m.ring)
	if m.n < len(m.ring) {
		m.n++
	}
	m.mu.Unlock()

	if m.rec != nil {
		if b, err := json.Marshal(s); err == nil {
			m.rec.Write(append(b, '\n'))
		}
	}

	m.subMu.Lock()
	for ch := range m.subs {
		select {
		case ch <- s:
		default: // slow consumer: drop
		}
	}
	m.subMu.Unlock()
}

func (m *Monitor) classify(s *Sample) {
	if s.RTTMs == nil {
		s.Status = StatusDown
		s.Score, s.MOS = 0, 1
		return
	}
	jit := 0.0
	if s.JitterMs != nil {
		jit = *s.JitterMs
	}
	r, mos := Quality(*s.RTTMs, jit, s.LossPct)
	s.Score, s.MOS = round2(r), round2(mos)
	s.Status = StatusGood
	q := m.cfg.Quality
	base, ok := m.BaselineRTT()
	spike := ok && *s.RTTMs > q.LatencySpikeMultiplier*base && *s.RTTMs > base+q.LatencySpikeMinIncreaseMs
	if s.LossPct > 0 || r < q.DegradedBelowScore || spike {
		s.Status = StatusDegraded
	}
}

func msPtr(d time.Duration) *float64 {
	return f64(math.Round(float64(d.Microseconds())/10) / 100)
}
