//go:build !darwin && !linux

package sysnet

func DefaultRoute() (Route, error)                { return Route{}, ErrUnsupported }
func ReadCounters(iface string) (Counters, error) { return Counters{}, ErrUnsupported }
