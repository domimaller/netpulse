package speed

import (
	"math"
	"testing"
	"time"
)

func TestSteadyRateIgnoresSlowStart(t *testing.T) {
	// 100ms of connect (0 bytes), then 100ms slow start at 10 Mbps,
	// then 400ms at 100 Mbps.
	var pts []point
	var b int64
	for ms := 0; ms <= 600; ms += 20 {
		pts = append(pts, point{time.Duration(ms) * time.Millisecond, b})
		switch {
		case ms < 100:
		case ms < 200:
			b += 10e6 / 8 / 50
		default:
			b += 100e6 / 8 / 50
		}
	}
	got := steadyRate(pts)
	if math.Abs(got-100e6)/100e6 > 0.1 {
		t.Fatalf("steady rate %.1f Mbps, want ~100", got/1e6)
	}
}

func TestSteadyRateDegenerate(t *testing.T) {
	if steadyRate(nil) != 0 || steadyRate([]point{{0, 0}, {time.Second, 0}}) != 0 {
		t.Fatal("expected 0")
	}
	// tiny, instant transfer: falls back to whole span
	if r := steadyRate([]point{{0, 0}, {20 * time.Millisecond, 125000}}); r <= 0 {
		t.Fatal("expected positive rate")
	}
}
