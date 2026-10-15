// netpulse: per-second internet quality monitor with a polite speed
// estimator and a local web UI. All settings live in a YAML config file;
// run `netpulse -print-default-config` to see every option with its description.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/domimaller/netpulse/internal/config"
	"github.com/domimaller/netpulse/internal/monitor"
	"github.com/domimaller/netpulse/internal/server"
	"github.com/domimaller/netpulse/internal/speed"
)

func main() {
	var (
		configPath   = flag.String("config", "", "path to the YAML config file (default: "+config.DefaultPath()+" if it exists)")
		printDefault = flag.Bool("print-default-config", false, "print the default config, with a description of every setting, and exit")
		checkOnly    = flag.Bool("check-config", false, "validate the config file and exit")
	)
	flag.Parse()

	if *printDefault {
		os.Stdout.Write(config.DefaultYAML)
		return
	}

	path, explicit := *configPath, true
	if path == "" {
		path, explicit = config.DefaultPath(), false
	}
	cfg, used, err := config.Load(config.ExpandHome(path), explicit)
	if err != nil {
		log.Fatal(err)
	}
	if *checkOnly {
		src := used
		if src == "" {
			src = "built-in defaults"
		}
		fmt.Printf("config OK (%s)\n", src)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mon, err := monitor.New(monitor.Config{
		Probes:  cfg.LatencyProbes,
		DNS:     cfg.DNSProbe,
		Quality: cfg.QualityRules,
		History: cfg.History,
	})
	if err != nil {
		log.Fatal(err)
	}
	est := speed.New(cfg.SpeedEstimator, mon)

	go mon.Run(ctx)
	go est.Run(ctx)

	srv := &http.Server{
		Addr:              cfg.WebServer.ListenAddress,
		Handler:           server.New(ctx, cfg, used, mon, est).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(sh)
	}()
	src := used
	if src == "" {
		src = "built-in defaults"
	}
	log.Printf("netpulse: config from %s; probing %v via %s; UI at http://%s",
		src, cfg.LatencyProbes.InternetTargets, mon.Pinger().Mode(), cfg.WebServer.ListenAddress)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
