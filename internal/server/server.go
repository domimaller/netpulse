// Package server exposes the monitor over HTTP: a JSON API, a Server-Sent
// Events stream, and the embedded web UI. The JSON API is also the contract
// a future macOS menu bar app will consume.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/domimaller/netpulse/internal/config"
	"github.com/domimaller/netpulse/internal/monitor"
	"github.com/domimaller/netpulse/internal/speed"
)

//go:embed web
var webFS embed.FS

type Server struct {
	mon     *monitor.Monitor
	est     *speed.Estimator
	ctx     context.Context // app lifetime, for background jobs
	cfg     config.Config
	cfgPath string

	cacheMu sync.Mutex
	cacheT  int64
	cache   Summary
}

func New(ctx context.Context, cfg config.Config, cfgPath string, mon *monitor.Monitor, est *speed.Estimator) *Server {
	return &Server{mon: mon, est: est, ctx: ctx, cfg: cfg, cfgPath: cfgPath}
}

// Summary is the compact payload for dashboards and the menu bar.
type Summary struct {
	Now     *monitor.Sample  `json:"now"`
	Windows []monitor.Window `json:"windows"`
	Speed   speed.State      `json:"speed"`
	Info    monitor.Info     `json:"info"`
	// Seconds since the last "down" second (or since start).
	StableForS int64 `json:"stable_for_s"`
}

var windows = []struct {
	label string
	secs  int
}{{"1m", 60}, {"5m", 300}, {"15m", 900}, {"1h", 3600}, {"6h", 21600}, {"24h", 86400}}

// summary is cached per sample so many SSE clients cost one computation.
func (s *Server) summary() Summary {
	last := s.mon.Last(1)
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if len(last) == 1 && last[0].T == s.cacheT && !s.est.State().Probing {
		c := s.cache
		c.Speed = s.est.State()
		return c
	}
	out := s.computeSummary()
	if out.Now != nil {
		s.cacheT, s.cache = out.Now.T, out
	}
	return out
}

func (s *Server) computeSummary() Summary {
	all := s.mon.Last(86400)
	out := Summary{Speed: s.est.State(), Info: s.mon.Info()}
	if len(all) > 0 {
		last := all[len(all)-1]
		out.Now = &last
		var lastDown int64 = out.Info.Started
		for i := len(all) - 1; i >= 0; i-- {
			if all[i].Status == monitor.StatusDown {
				lastDown = all[i].T + 1000
				break
			}
		}
		out.StableForS = (last.T + 1000 - lastDown) / 1000
	}
	for i, w := range windows {
		if i > 0 && windows[i-1].secs >= len(all) {
			break // don't show longer windows than the data we have
		}
		n := w.secs
		if n > len(all) {
			n = len(all)
		}
		out.Windows = append(out.Windows, monitor.Summarize(w.label, w.secs, all[len(all)-n:], s.mon.QualityRules()))
	}
	return out
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", http.FileServer(http.FS(sub)))

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	mux.HandleFunc("GET /api/summary", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.summary())
	})

	// /api/samples?seconds=300  or  ?since=<unix ms>
	mux.HandleFunc("GET /api/samples", func(w http.ResponseWriter, r *http.Request) {
		secs, _ := strconv.Atoi(r.URL.Query().Get("seconds"))
		if secs <= 0 {
			secs = 300
		}
		if since, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64); err == nil {
			writeJSON(w, s.mon.Since(since, secs))
			return
		}
		writeJSON(w, s.mon.Last(secs))
	})

	// Effective configuration (durations in nanoseconds) and where it came from.
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		src := s.cfgPath
		if src == "" {
			src = "built-in defaults"
		}
		writeJSON(w, map[string]any{"source": src, "config": s.cfg})
	})

	mux.HandleFunc("GET /api/speed", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.est.State())
	})

	mux.HandleFunc("POST /api/speedtest", func(w http.ResponseWriter, r *http.Request) {
		if err := s.est.FullTest(s.ctx); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, map[string]string{"status": "started"})
	})

	// Server-Sent Events: one "tick" event per second with the new sample
	// and a fresh summary.
	mux.HandleFunc("GET /api/stream", func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		ch, unsub := s.mon.Subscribe()
		defer unsub()
		ping := time.NewTicker(15 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ping.C:
				w.Write([]byte(": ping\n\n"))
				fl.Flush()
			case smp := <-ch:
				b, _ := json.Marshal(struct {
					Sample  monitor.Sample `json:"sample"`
					Summary Summary        `json:"summary"`
				}{smp, s.summary()})
				w.Write([]byte("event: tick\ndata: "))
				w.Write(b)
				w.Write([]byte("\n\n"))
				fl.Flush()
			}
		}
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	enc := json.NewEncoder(w)
	enc.Encode(v)
}
