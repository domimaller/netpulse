// Package speed estimates link capacity without getting in your way.
// All knobs live in the speed_estimator section of the config file.
//
// Politeness rules (all must hold before an automatic probe starts):
//
//  1. You are not using the network: your own traffic (interface counters
//     minus netpulse's traffic) stayed below idle_traffic_threshold_kbps in
//     both directions for idle_required_for. If nobody is using the link,
//     nobody can feel a short probe.
//  2. The network is healthy: no loss / outage recently and latency within
//     max_latency_above_baseline_to_start_ms of its unloaded baseline.
//  3. Probes are short: sized from the previous estimate to last about
//     target_probe_duration, never more than max_probe_size_mb.
//  4. A latency guard pings every 100ms during the transfer; if latency rises
//     by abort_when_latency_rises_ms (queues are filling, i.e. you *would*
//     feel it) the transfer is cancelled immediately.
//  5. Probes are rare and adaptive: the interval starts at min_probe_interval
//     and grows by interval_growth_factor whenever the result is unchanged,
//     up to max_probe_interval. A changed result or network change resets it.
//  6. daily_data_budget_mb caps the total.
//
// Between probes, the busiest second of *your* real traffic within
// passive_peak_window is used as a free lower bound for capacity.
package speed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/domimaller/netpulse/internal/config"
	"github.com/domimaller/netpulse/internal/monitor"
)

// Config is the speed_estimator section of the config file.
type Config = config.SpeedEstimator

const mb = 1 << 20

// Result of one transfer.
type Result struct {
	T           int64    `json:"t"`
	Dir         string   `json:"dir"` // down | up
	Full        bool     `json:"full"`
	Bytes       int64    `json:"bytes"`
	DurationMs  float64  `json:"duration_ms"`
	Bps         float64  `json:"bps"`
	LoadedRTTMs *float64 `json:"loaded_rtt_ms"` // median RTT measured while the transfer ran
	BaselineMs  *float64 `json:"baseline_ms"`
	Aborted     string   `json:"aborted,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// State is what the API exposes.
type State struct {
	Enabled bool `json:"enabled"`

	DownBps *float64 `json:"down_bps"` // smoothed active estimate
	UpBps   *float64 `json:"up_bps"`
	// Busiest second of your own traffic in the last 10 minutes: a free
	// lower bound on capacity.
	PassivePeakRxBps float64 `json:"passive_peak_rx_bps"`
	PassivePeakTxBps float64 `json:"passive_peak_tx_bps"`
	// max(active estimate, passive peak)
	CapacityDownBps *float64 `json:"capacity_down_bps"`
	CapacityUpBps   *float64 `json:"capacity_up_bps"`

	BufferbloatMs *float64 `json:"bufferbloat_ms"` // loaded RTT - baseline during last download

	Status      string   `json:"status"` // human readable
	Probing     bool     `json:"probing"`
	FullRunning bool     `json:"full_running"`
	LastProbeAt int64    `json:"last_probe_at"`
	NextProbeAt int64    `json:"next_probe_at"`
	IntervalS   float64  `json:"interval_s"`
	BudgetBytes int64    `json:"budget_bytes"`
	UsedTodayB  int64    `json:"used_today_bytes"`
	ProbesToday int      `json:"probes_today"`
	LastFull    []Result `json:"last_full"`
	Recent      []Result `json:"recent"`
}

type Estimator struct {
	cfg Config
	mon *monitor.Monitor
	hc  *http.Client

	rxAcc, txAcc atomic.Uint64
	active       atomic.Bool
	ranSinceTake atomic.Bool

	mu          sync.Mutex
	down, up    *float64
	bloat       *float64
	status      string
	probing     bool
	fullRunning bool
	lastAt      time.Time
	nextAt      time.Time
	interval    time.Duration
	day         string
	usedToday   int64
	probesToday int
	recent      []Result
	lastFull    []Result
	netGen      uint64
	session     int
	busy        sync.Mutex // one transfer at a time
}

func New(cfg Config, mon *monitor.Monitor) *Estimator {
	e := &Estimator{
		cfg: cfg, mon: mon,
		hc: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true,
		}},
		interval: cfg.MinProbeInterval,
		status:   "warming up (collecting a latency baseline)",
	}
	// First probe shortly after start, once the baseline exists.
	e.nextAt = time.Now().Add(cfg.FirstProbeDelay)
	mon.SetAccounting(e)
	return e
}

// TakeBytes implements monitor.ProbeAccounting.
func (e *Estimator) TakeBytes() (uint64, uint64, bool) {
	active := e.active.Load() || e.ranSinceTake.Swap(false)
	return e.rxAcc.Swap(0), e.txAcc.Swap(0), active
}

func (e *Estimator) State() State {
	last := e.mon.Last(int(e.cfg.PassivePeakWindow / time.Second))
	var pr, pt float64
	for _, s := range last {
		if s.SpeedProbe {
			continue
		}
		pr = math.Max(pr, s.RxBps)
		pt = math.Max(pt, s.TxBps)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	st := State{
		Enabled: e.cfg.AutomaticProbesEnabled,
		DownBps: e.down, UpBps: e.up,
		PassivePeakRxBps: pr, PassivePeakTxBps: pt,
		BufferbloatMs: e.bloat,
		Status:        e.status, Probing: e.probing, FullRunning: e.fullRunning,
		IntervalS:   e.interval.Seconds(),
		BudgetBytes: int64(e.cfg.DailyDataBudgetMB * mb), UsedTodayB: e.usedToday, ProbesToday: e.probesToday,
		Recent:   append([]Result(nil), e.recent...),
		LastFull: append([]Result(nil), e.lastFull...),
	}
	if !e.lastAt.IsZero() {
		st.LastProbeAt = e.lastAt.UnixMilli()
	}
	if e.cfg.AutomaticProbesEnabled {
		st.NextProbeAt = e.nextAt.UnixMilli()
	}
	st.CapacityDownBps = maxPtr(e.down, pr)
	st.CapacityUpBps = maxPtr(e.up, pt)
	return st
}

func maxPtr(p *float64, v float64) *float64 {
	if p == nil && v == 0 {
		return nil
	}
	m := v
	if p != nil && *p > m {
		m = *p
	}
	return &m
}

func (e *Estimator) setStatus(s string) {
	e.mu.Lock()
	e.status = s
	e.mu.Unlock()
}

// Run drives automatic probes until ctx is cancelled.
func (e *Estimator) Run(ctx context.Context) {
	if !e.cfg.AutomaticProbesEnabled {
		e.setStatus("automatic probes disabled")
		return
	}
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			e.maybeProbe(ctx, now)
		}
	}
}

func (e *Estimator) maybeProbe(ctx context.Context, now time.Time) {
	e.mu.Lock()
	if g := e.mon.NetworkGeneration(); g != e.netGen {
		if e.netGen != 0 {
			// New network: old numbers are meaningless, re-measure soon.
			e.down, e.up, e.bloat = nil, nil, nil
			e.interval = e.cfg.MinProbeInterval
			e.nextAt = now.Add(15 * time.Second)
			e.status = "network changed, re-measuring soon"
		}
		e.netGen = g
	}
	if day := now.Format("2006-01-02"); day != e.day {
		e.day, e.usedToday, e.probesToday = day, 0, 0
	}
	due := !now.Before(e.nextAt)
	overBudget := e.usedToday >= int64(e.cfg.DailyDataBudgetMB*mb)
	e.mu.Unlock()
	if !due {
		return
	}
	if overBudget {
		e.setStatus("daily probe budget used up; using passive estimate only")
		e.deferBy(now, 10*time.Minute)
		return
	}
	if ok, why := e.gates(); !ok {
		e.setStatus("waiting: " + why)
		e.deferBy(now, 5*time.Second)
		return
	}
	e.session++
	e.politeSession(ctx, "down")
	// Upload only every Nth session: uplinks are the easiest to hurt. 0 = never.
	if n := e.cfg.UploadProbeEveryNthSession; n > 0 && (e.session-1)%n == 0 {
		if ok, _ := e.gates(); ok {
			e.politeSession(ctx, "up")
		}
	}
}

func (e *Estimator) deferBy(now time.Time, d time.Duration) {
	e.mu.Lock()
	e.nextAt = now.Add(d)
	e.mu.Unlock()
}

// gates checks politeness rules 1 and 2.
func (e *Estimator) gates() (bool, string) {
	base, ok := e.mon.BaselineRTT()
	if !ok {
		return false, "collecting latency baseline"
	}
	idleSeconds := int(e.cfg.IdleRequiredFor / time.Second)
	idleBps := e.cfg.IdleTrafficThresholdKbps * 1000
	recent := e.mon.Last(idleSeconds)
	if len(recent) < idleSeconds {
		return false, "collecting traffic history"
	}
	for _, s := range recent {
		if s.SpeedProbe {
			continue
		}
		if s.RxBps > idleBps || s.TxBps > idleBps {
			return false, fmt.Sprintf("you are using the network (%.1f↓ / %.1f↑ Mbps)", s.RxBps/1e6, s.TxBps/1e6)
		}
		if s.Status == monitor.StatusDown || s.LossPct > 0 {
			return false, "recent packet loss"
		}
		if s.RTTMs != nil && *s.RTTMs > base+e.cfg.MaxLatencyAboveBaselineToStartMs {
			return false, fmt.Sprintf("latency above baseline (%.0f ms vs %.0f ms)", *s.RTTMs, base)
		}
	}
	return true, ""
}

// politeSession sizes a probe from the last estimate and grows it (at most
// twice) if the transfer finished too quickly to be meaningful.
func (e *Estimator) politeSession(ctx context.Context, dir string) {
	e.mu.Lock()
	prev := e.down
	if dir == "up" {
		prev = e.up
	}
	e.probing = true
	e.status = "probing " + dir + "load"
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.probing = false
		e.mu.Unlock()
	}()

	target := e.cfg.TargetProbeDuration
	size := int64(e.cfg.MinProbeSizeMB*mb) * 2
	if prev != nil {
		size = int64(*prev / 8 * target.Seconds())
	}
	var res Result
	for attempt := 0; attempt < 3; attempt++ {
		size = clamp(size, int64(e.cfg.MinProbeSizeMB*mb), int64(e.cfg.MaxProbeSizeMB*mb))
		res = e.transfer(ctx, dir, size, 1, 0, true)
		e.account(res)
		if res.Error != "" || res.Aborted != "" || res.Bps <= 0 {
			break
		}
		if time.Duration(res.DurationMs*float64(time.Millisecond)) >= target/2 || size >= int64(e.cfg.MaxProbeSizeMB*mb) {
			break
		}
		if ok, _ := e.gates(); !ok {
			break
		}
		// Too short to trust (TCP still in slow start): size up to hit the target.
		size = int64(res.Bps / 8 * target.Seconds() * 1.5)
	}
	e.record(res, prev)
}

func clamp(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (e *Estimator) account(r Result) {
	e.mu.Lock()
	e.usedToday += r.Bytes
	e.mu.Unlock()
}

func (e *Estimator) record(r Result, prev *float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	e.lastAt = now
	e.probesToday++
	e.recent = append(e.recent, r)
	if len(e.recent) > 40 {
		e.recent = e.recent[len(e.recent)-40:]
	}
	switch {
	case r.Error != "":
		e.status = "last probe failed: " + r.Error
		e.interval = e.cfg.MinProbeInterval
	case r.Aborted != "":
		e.status = "last probe stopped early: " + r.Aborted
		// Your link is sensitive to load: back off harder.
		e.interval = minDur(e.interval*2, e.cfg.MaxProbeInterval)
	default:
		cur := &e.down
		if r.Dir == "up" {
			cur = &e.up
		}
		if *cur == nil {
			v := r.Bps
			*cur = &v
		} else {
			old := **cur
			v := 0.5*old + 0.5*r.Bps
			*cur = &v
			if 100*math.Abs(r.Bps-old)/old < e.cfg.StableResultTolerancePct {
				e.interval = minDur(time.Duration(float64(e.interval)*e.cfg.IntervalGrowthFactor), e.cfg.MaxProbeInterval)
			} else {
				e.interval = e.cfg.MinProbeInterval
			}
		}
		_ = prev
		if r.Dir == "down" && r.LoadedRTTMs != nil && r.BaselineMs != nil {
			b := math.Max(0, *r.LoadedRTTMs-*r.BaselineMs)
			e.bloat = &b
		}
		e.status = fmt.Sprintf("idle; last %sload probe %.1f Mbps", r.Dir, r.Bps/1e6)
	}
	e.nextAt = now.Add(e.interval)
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// FullTest runs an explicit, user-requested test: 4 parallel streams for a
// few seconds per direction. It does not abort on latency, but reports the
// loaded latency (bufferbloat) it caused.
func (e *Estimator) FullTest(ctx context.Context) error {
	e.mu.Lock()
	if e.fullRunning {
		e.mu.Unlock()
		return errors.New("a full test is already running")
	}
	e.fullRunning = true
	e.status = "running full speed test"
	e.mu.Unlock()
	go func() {
		defer func() {
			e.mu.Lock()
			e.fullRunning = false
			e.mu.Unlock()
		}()
		var out []Result
		for _, dir := range []string{"down", "up"} {
			// Uploads are only measured on completion, so keep each request
			// short (~250ms per stream) to avoid losing a big unfinished tail.
			chunk := int64(25 * mb)
			if dir == "up" {
				chunk = 2 * mb
				e.mu.Lock()
				if e.up != nil {
					chunk = clamp(int64(*e.up/8*0.25), 256<<10, 25*mb)
				}
				e.mu.Unlock()
			}
			r := e.transfer(ctx, dir, chunk, e.cfg.FullTestParallelStreams, e.cfg.FullTestDurationPerDirection, false)
			r.Full = true
			out = append(out, r)
		}
		e.mu.Lock()
		e.lastFull = out
		e.recent = append(e.recent, out...)
		for _, r := range out {
			if r.Error != "" || r.Bps <= 0 {
				continue
			}
			v := r.Bps
			if r.Dir == "down" {
				e.down = &v
				if r.LoadedRTTMs != nil && r.BaselineMs != nil {
					b := math.Max(0, *r.LoadedRTTMs-*r.BaselineMs)
					e.bloat = &b
				}
			} else {
				e.up = &v
			}
		}
		e.lastAt = time.Now()
		e.status = "full test finished"
		e.mu.Unlock()
		log.Printf("full test: %+v", out)
	}()
	return nil
}

type point struct {
	t time.Duration
	b int64
}

// transfer moves bytes in one direction and measures steady-state
// throughput. streams>1 with dur>0 repeats requests until dur elapses.
func (e *Estimator) transfer(parent context.Context, dir string, size int64, streams int, dur time.Duration, guard bool) Result {
	e.busy.Lock()
	defer e.busy.Unlock()
	e.active.Store(true)
	e.ranSinceTake.Store(true)
	defer e.active.Store(false)

	res := Result{T: time.Now().UnixMilli(), Dir: dir}
	base, hasBase := e.mon.BaselineRTT()
	if hasBase {
		res.BaselineMs = &base
	}

	timeout := 15 * time.Second
	if dur > 0 {
		timeout = dur + 5*time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if dur > 0 {
		var c2 context.CancelFunc
		ctx, c2 = context.WithTimeout(ctx, dur)
		defer c2()
	}

	var moved atomic.Int64
	start := time.Now()
	// Upload bookkeeping: bytes handed to the socket are not bytes on the
	// wire (send buffers absorb megabytes), so uploads are measured from the
	// first body byte to the last completed request.
	up := &upStats{start: start}

	// Sampler for the throughput curve.
	var pts []point
	var ptsMu sync.Mutex
	stopSampler := make(chan struct{})
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		tk := time.NewTicker(20 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stopSampler:
				return
			case <-tk.C:
				ptsMu.Lock()
				pts = append(pts, point{time.Since(start), moved.Load()})
				ptsMu.Unlock()
			}
		}
	}()

	// Latency guard: ping while transferring.
	var loaded []float64
	var abortReason atomic.Value
	guardDone := make(chan struct{})
	go func() {
		defer close(guardDone)
		info := e.mon.Info()
		if len(info.Targets) == 0 {
			return
		}
		host := info.Targets[0]
		over := 0
		tk := time.NewTicker(100 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				pctx, pc := context.WithTimeout(ctx, 500*time.Millisecond)
				rtt, err := e.mon.Pinger().Ping(pctx, host)
				pc()
				if err != nil {
					continue
				}
				ms := float64(rtt.Microseconds()) / 1000
				loaded = append(loaded, ms)
				if guard && hasBase && ms > base+e.cfg.AbortWhenLatencyRisesMs {
					over++
					if over >= 2 {
						abortReason.Store(fmt.Sprintf("latency rose to %.0f ms (baseline %.0f ms)", ms, base))
						cancel()
						return
					}
				} else {
					over = 0
				}
			}
		}
	}()

	var wg sync.WaitGroup
	var firstErr atomic.Value
	for i := 0; i < streams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				var err error
				if dir == "down" {
					err = e.download(ctx, size, &moved)
				} else {
					err = e.upload(ctx, size, &moved, up)
				}
				if err != nil && ctx.Err() == nil {
					firstErr.CompareAndSwap(nil, err.Error())
					return
				}
				if dur == 0 || ctx.Err() != nil {
					return
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	cancel()
	close(stopSampler)
	<-samplerDone
	<-guardDone

	res.Bytes = moved.Load()
	res.DurationMs = math.Round(float64(elapsed.Microseconds())/10) / 100
	if dir == "up" {
		res.Bytes = up.sent.Load()
		if f, l := up.firstByte.Load(), up.lastDone.Load(); l > f && f > 0 {
			res.Bps = float64(moved.Load()) * 8 / time.Duration(l-f).Seconds()
		}
	} else {
		ptsMu.Lock()
		pts = append(pts, point{elapsed, res.Bytes})
		res.Bps = steadyRate(pts)
		ptsMu.Unlock()
	}
	if len(loaded) > 0 {
		sort.Float64s(loaded)
		m := loaded[len(loaded)/2]
		res.LoadedRTTMs = &m
	}
	if v, ok := abortReason.Load().(string); ok {
		res.Aborted = v
	}
	if v, ok := firstErr.Load().(string); ok && res.Aborted == "" {
		res.Error = v
	}
	return res
}

// steadyRate ignores the first 30% of the transfer (TCP slow start, TLS
// setup) and returns bits/s over the rest.
func steadyRate(pts []point) float64 {
	if len(pts) < 2 {
		return 0
	}
	end := pts[len(pts)-1]
	if end.t <= 0 || end.b == 0 {
		return 0
	}
	// first point with any bytes = transfer start (excludes connect/TLS)
	i0 := 0
	for i0 < len(pts)-1 && pts[i0].b == 0 {
		i0++
	}
	if i0 > 0 {
		i0--
	}
	t0 := pts[i0].t
	cut := t0 + (end.t-t0)*3/10
	j := i0
	for j < len(pts)-1 && pts[j].t < cut {
		j++
	}
	s := pts[j]
	if end.t-s.t < 30*time.Millisecond || end.b-s.b <= 0 {
		s = pts[i0]
	}
	dt := (end.t - s.t).Seconds()
	if dt <= 0 {
		return 0
	}
	return float64(end.b-s.b) * 8 / dt
}

func (e *Estimator) download(ctx context.Context, size int64, moved *atomic.Int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(e.cfg.DownloadURL, size), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "netpulse/0.1")
	resp, err := e.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	buf := make([]byte, 64<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			moved.Add(int64(n))
			e.rxAcc.Add(uint64(float64(n) * 1.03)) // + TLS/TCP overhead
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type upStats struct {
	start     time.Time
	firstByte atomic.Int64 // ns since start of first body byte read
	lastDone  atomic.Int64 // ns since start of last completed request
	sent      atomic.Int64 // bytes handed to the socket (incl. unfinished requests)
}

type countingReader struct {
	left int64
	up   *upStats
	acc  *atomic.Uint64
}

var zeros = make([]byte, 64<<10)

func (r *countingReader) Read(p []byte) (int, error) {
	if r.left <= 0 {
		return 0, io.EOF
	}
	r.up.firstByte.CompareAndSwap(0, int64(time.Since(r.up.start)))
	n := int64(len(p))
	if n > r.left {
		n = r.left
	}
	if n > int64(len(zeros)) {
		n = int64(len(zeros))
	}
	copy(p, zeros[:n])
	r.left -= n
	r.up.sent.Add(n)
	r.acc.Add(uint64(float64(n) * 1.03))
	return int(n), nil
}

func (e *Estimator) upload(ctx context.Context, size int64, moved *atomic.Int64, up *upStats) error {
	body := &countingReader{left: size, up: up, acc: &e.txAcc}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.UploadURL, body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("User-Agent", "netpulse/0.1")
	resp, err := e.hc.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("upload: HTTP %d", resp.StatusCode)
	}
	moved.Add(size)
	up.lastDone.Store(int64(time.Since(up.start)))
	return nil
}
