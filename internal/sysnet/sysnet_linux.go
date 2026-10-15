//go:build linux

package sysnet

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// DefaultRoute parses /proc/net/route.
func DefaultRoute() (Route, error) {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return Route{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		x := strings.Fields(sc.Text())
		if len(x) < 3 || x[1] != "00000000" {
			continue
		}
		b, err := hex.DecodeString(x[2])
		if err != nil || len(b) != 4 {
			continue
		}
		ip := net.IPv4(b[3], b[2], b[1], b[0])
		if binary.LittleEndian.Uint32(b) == 0 {
			return Route{Interface: x[0]}, nil
		}
		return Route{Gateway: ip.String(), Interface: x[0]}, nil
	}
	return Route{}, fmt.Errorf("no default route")
}

// ReadCounters parses /proc/net/dev.
func ReadCounters(iface string) (Counters, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return Counters{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok || strings.TrimSpace(name) != iface {
			continue
		}
		x := strings.Fields(rest)
		if len(x) < 9 {
			break
		}
		rx, _ := strconv.ParseUint(x[0], 10, 64)
		tx, _ := strconv.ParseUint(x[8], 10, 64)
		return Counters{RxBytes: rx, TxBytes: tx}, nil
	}
	return Counters{}, fmt.Errorf("interface %s not found", iface)
}
