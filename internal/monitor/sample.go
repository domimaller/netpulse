package monitor

import (
	"math"

	"github.com/domimaller/netpulse/internal/config"
	"sort"
)

// Status of one second.
const (
	StatusGood     = "good"
	StatusDegraded = "degraded"
	StatusDown     = "down"
)

// Sample is everything measured during one wall-clock second.
type Sample struct {
	T int64 `json:"t"` // unix milliseconds of the start of the second

	RTTMs     *float64            `json:"rtt_ms"`     // median RTT across internet targets
	TargetRTT map[string]*float64 `json:"target_rtt"` // per-target RTT (null = lost)
	JitterMs  *float64            `json:"jitter_ms"`  // mean |ΔRTT| vs previous second, per target
	LossPct   float64             `json:"loss_pct"`   // lost / sent this second
	GatewayMs *float64            `json:"gateway_ms"` // local router RTT (Wi-Fi / LAN health)
	DNSMs     *float64            `json:"dns_ms"`     // resolver lookup time

	RxBps float64 `json:"rx_bps"` // bits/s actually used on the default interface
	TxBps float64 `json:"tx_bps"`

	Score  float64 `json:"score"`  // 0..100 call-quality style score (R-factor)
	MOS    float64 `json:"mos"`    // 1..4.5
	Status string  `json:"status"` // good | degraded | down

	SpeedProbe bool `json:"speed_probe,omitempty"` // an active throughput probe ran during this second
}

func f64(v float64) *float64 { return &v }

// Quality computes an E-model style R-factor and MOS from latency, jitter
// and loss. This is the same heuristic used by many VoIP / network
// monitoring tools: it weights what you actually *feel* on calls and in
// interactive apps.
func Quality(rttMs, jitterMs, lossPct float64) (r, mos float64) {
	eff := rttMs + 2*jitterMs + 10 // effective latency
	if eff < 160 {
		r = 93.2 - eff/40
	} else {
		r = 93.2 - (eff-120)/10
	}
	r -= 2.5 * lossPct
	r = math.Max(0, math.Min(100, r))
	mos = 1 + 0.035*r + 0.000007*r*(r-60)*(100-r)
	mos = math.Max(1, math.Min(4.5, mos))
	return r, mos
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// percentile with linear interpolation; xs need not be sorted.
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if len(s) == 1 {
		return s[0]
	}
	pos := p / 100 * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return s[lo] + (s[hi]-s[lo])*(pos-float64(lo))
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	var t float64
	for _, x := range xs {
		t += x
	}
	return t / float64(len(xs))
}

// Window summarises a span of samples.
type Window struct {
	Label   string `json:"label"`
	Seconds int    `json:"seconds"`
	Samples int    `json:"samples"`

	AvailabilityPct float64 `json:"availability_pct"` // seconds not "down"
	GoodPct         float64 `json:"good_pct"`         // seconds "good"
	DownSeconds     int     `json:"down_seconds"`
	DegradedSeconds int     `json:"degraded_seconds"`
	Outages         int     `json:"outages"` // distinct runs of down seconds
	LongestOutageS  int     `json:"longest_outage_s"`
	Spikes          int     `json:"spikes"` // seconds with RTT > 2x window median (and > +30ms)

	RTTMinMs   *float64 `json:"rtt_min_ms"`
	RTTP50Ms   *float64 `json:"rtt_p50_ms"`
	RTTP95Ms   *float64 `json:"rtt_p95_ms"`
	RTTP99Ms   *float64 `json:"rtt_p99_ms"`
	JitterMs   *float64 `json:"jitter_ms"`
	LossPct    float64  `json:"loss_pct"`
	GatewayP50 *float64 `json:"gateway_p50_ms"`
	GatewayP95 *float64 `json:"gateway_p95_ms"`
	DNSP50Ms   *float64 `json:"dns_p50_ms"`

	ScoreAvg float64 `json:"score_avg"`
	MOSAvg   float64 `json:"mos_avg"`

	RxAvgBps  float64 `json:"rx_avg_bps"`
	TxAvgBps  float64 `json:"tx_avg_bps"`
	RxPeakBps float64 `json:"rx_peak_bps"`
	TxPeakBps float64 `json:"tx_peak_bps"`
}

func ptr(v float64) *float64 {
	if math.IsNaN(v) {
		return nil
	}
	return f64(math.Round(v*100) / 100)
}

// Summarize builds a Window from samples (oldest first).
// Spikes use the same quality rules as per-second classification, relative
// to the window's median latency.
func Summarize(label string, seconds int, ss []Sample, q config.QualityRules) Window {
	w := Window{Label: label, Seconds: seconds, Samples: len(ss)}
	if len(ss) == 0 {
		return w
	}
	var rtts, jit, gw, dns []float64
	var loss, score, mos, rx, tx float64
	run := 0
	for _, s := range ss {
		switch s.Status {
		case StatusDown:
			w.DownSeconds++
			run++
			if run == 1 {
				w.Outages++
			}
			if run > w.LongestOutageS {
				w.LongestOutageS = run
			}
		default:
			run = 0
			if s.Status == StatusDegraded {
				w.DegradedSeconds++
			}
		}
		if s.RTTMs != nil {
			rtts = append(rtts, *s.RTTMs)
		}
		if s.JitterMs != nil {
			jit = append(jit, *s.JitterMs)
		}
		if s.GatewayMs != nil {
			gw = append(gw, *s.GatewayMs)
		}
		if s.DNSMs != nil {
			dns = append(dns, *s.DNSMs)
		}
		loss += s.LossPct
		score += s.Score
		mos += s.MOS
		rx += s.RxBps
		tx += s.TxBps
		w.RxPeakBps = math.Max(w.RxPeakBps, s.RxBps)
		w.TxPeakBps = math.Max(w.TxPeakBps, s.TxBps)
	}
	n := float64(len(ss))
	w.AvailabilityPct = round2(100 * (n - float64(w.DownSeconds)) / n)
	w.GoodPct = round2(100 * (n - float64(w.DownSeconds+w.DegradedSeconds)) / n)
	w.LossPct = round2(loss / n)
	w.ScoreAvg = round2(score / n)
	w.MOSAvg = round2(mos / n)
	w.RxAvgBps, w.TxAvgBps = math.Round(rx/n), math.Round(tx/n)
	if len(rtts) > 0 {
		sorted := append([]float64(nil), rtts...)
		sort.Float64s(sorted)
		w.RTTMinMs = ptr(sorted[0])
		med := median(rtts)
		w.RTTP50Ms = ptr(med)
		w.RTTP95Ms = ptr(percentile(rtts, 95))
		w.RTTP99Ms = ptr(percentile(rtts, 99))
		for _, r := range rtts {
			if r > q.LatencySpikeMultiplier*med && r > med+q.LatencySpikeMinIncreaseMs {
				w.Spikes++
			}
		}
	}
	w.JitterMs = ptr(mean(jit))
	w.GatewayP50 = ptr(median(gw))
	w.GatewayP95 = ptr(percentile(gw, 95))
	w.DNSP50Ms = ptr(median(dns))
	return w
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
