//go:build darwin

package sysnet

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func run(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// DefaultRoute parses `route -n get default`.
func DefaultRoute() (Route, error) {
	out, err := run("/sbin/route", "-n", "get", "default")
	if err != nil {
		return Route{}, err
	}
	return parseRouteGet(out), nil
}

func parseRouteGet(out []byte) Route {
	var r Route
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "gateway":
			r.Gateway = strings.TrimSpace(v)
		case "interface":
			r.Interface = strings.TrimSpace(v)
		}
	}
	return r
}

// ReadCounters parses `netstat -I <iface> -b -n` (link-level row).
func ReadCounters(iface string) (Counters, error) {
	out, err := run("/usr/sbin/netstat", "-I", iface, "-b", "-n")
	if err != nil {
		return Counters{}, err
	}
	return parseNetstat(out, iface)
}

func parseNetstat(out []byte, iface string) (Counters, error) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	var header []string
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if f[0] == "Name" {
			header = f
			continue
		}
		if header == nil || f[0] != iface || len(f) < 3 || !strings.HasPrefix(f[2], "<Link#") {
			continue
		}
		// The Address column may be empty (e.g. utun), shifting columns
		// left by one. Index from the right instead: ... Ibytes Opkts Oerrs Obytes Coll
		ib, oi := len(header)-5, len(header)-2
		if len(f) == len(header)-1 {
			ib, oi = ib-1, oi-1
		}
		if ib < 0 || oi >= len(f) {
			break
		}
		rx, err1 := strconv.ParseUint(f[ib], 10, 64)
		tx, err2 := strconv.ParseUint(f[oi], 10, 64)
		if err1 != nil || err2 != nil {
			return Counters{}, fmt.Errorf("parse netstat: %v %v", err1, err2)
		}
		return Counters{RxBytes: rx, TxBytes: tx}, nil
	}
	return Counters{}, fmt.Errorf("interface %s not found in netstat output", iface)
}
