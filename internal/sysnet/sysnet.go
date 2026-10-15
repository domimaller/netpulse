// Package sysnet reads the default route and per-interface byte counters.
// Both are free (no network traffic): they let netpulse see how much the
// machine is already using the link, which is what makes the speed
// estimator polite.
package sysnet

import "errors"

// Route describes the current default route.
type Route struct {
	Gateway   string `json:"gateway"`
	Interface string `json:"interface"`
}

// Counters are cumulative byte counters of one interface.
type Counters struct {
	RxBytes uint64
	TxBytes uint64
}

var ErrUnsupported = errors.New("not supported on this platform")
