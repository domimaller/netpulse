package monitor

import (
	"testing"
	"time"

	"github.com/domimaller/netpulse/internal/config"
)

func TestQualityOrdering(t *testing.T) {
	good, _ := Quality(15, 1, 0)
	mid, _ := Quality(120, 20, 0)
	bad, _ := Quality(300, 50, 5)
	if !(good > 90 && good > mid && mid > bad) {
		t.Fatalf("scores not ordered: %.1f %.1f %.1f", good, mid, bad)
	}
}

func TestSummarizeOutages(t *testing.T) {
	q := config.QualityRules{LatencySpikeMultiplier: 3, LatencySpikeMinIncreaseMs: 50, BaselineWindow: 5 * time.Minute}
	mk := func(st string, rtt float64) Sample {
		s := Sample{Status: st}
		if st != StatusDown {
			s.RTTMs = f64(rtt)
		}
		return s
	}
	ss := []Sample{mk("good", 20), mk("down", 0), mk("down", 0), mk("good", 20), mk("degraded", 200), mk("down", 0), mk("good", 22)}
	w := Summarize("t", 7, ss, q)
	if w.DownSeconds != 3 || w.Outages != 2 || w.LongestOutageS != 2 || w.DegradedSeconds != 1 {
		t.Fatalf("got %+v", w)
	}
	if w.Spikes != 1 {
		t.Fatalf("spikes = %d", w.Spikes)
	}
	if w.AvailabilityPct < 57 || w.AvailabilityPct > 57.2 {
		t.Fatalf("availability %.2f", w.AvailabilityPct)
	}
}
