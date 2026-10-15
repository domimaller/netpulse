// Package probe implements tiny, cheap reachability/latency probes.
//
// Each probe costs a handful of bytes on the wire (one ICMP echo, or one TCP
// handshake), so running a few of them every second has no measurable impact
// on real traffic.
package probe

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// Pinger measures round-trip time to a host.
type Pinger interface {
	// Ping returns the RTT to host, or an error on timeout / failure.
	Ping(ctx context.Context, host string) (time.Duration, error)
	// Mode is a short human readable label ("icmp", "icmp-raw", "tcp:443").
	Mode() string
}

// New returns a pinger for method "auto", "icmp" or "tcp". auto picks
// unprivileged ICMP (works on macOS without root), then privileged ICMP,
// then TCP-connect timing.
func New(method string, tcpPort int) (Pinger, error) {
	if method == "tcp" {
		return &TCPPinger{Port: tcpPort}, nil
	}
	for _, network := range []string{"udp4", "ip4:icmp"} {
		c, err := icmp.ListenPacket(network, "0.0.0.0")
		if err == nil {
			c.Close()
			label := "icmp"
			if network == "ip4:icmp" {
				label = "icmp-raw"
			}
			return &ICMPPinger{network: network, label: label}, nil
		}
	}
	if method == "icmp" {
		return nil, errors.New("latency_probes.probe_method is icmp but this OS does not allow ICMP sockets for this user")
	}
	return &TCPPinger{Port: tcpPort}, nil
}

// ICMPPinger sends ICMP echo requests. With network "udp4" it uses the
// unprivileged datagram ICMP socket supported by macOS (and Linux when
// net.ipv4.ping_group_range allows it).
type ICMPPinger struct {
	network string
	label   string
}

var seq atomic.Uint32

func (p *ICMPPinger) Mode() string { return p.label }

func (p *ICMPPinger) Ping(ctx context.Context, host string) (time.Duration, error) {
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil {
		return 0, fmt.Errorf("icmp pinger needs an IPv4 literal, got %q", host)
	}
	c, err := icmp.ListenPacket(p.network, "0.0.0.0")
	if err != nil {
		return 0, err
	}
	defer c.Close()

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(time.Second)
	}
	_ = c.SetDeadline(deadline)

	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	s := int(seq.Add(1) & 0xffff)
	id := os.Getpid() & 0xffff
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho, Code: 0,
		Body: &icmp.Echo{ID: id, Seq: s, Data: nonce},
	}
	wb, err := msg.Marshal(nil)
	if err != nil {
		return 0, err
	}

	var dst net.Addr = &net.IPAddr{IP: ip}
	if p.network == "udp4" {
		dst = &net.UDPAddr{IP: ip}
	}

	start := time.Now()
	if _, err := c.WriteTo(wb, dst); err != nil {
		return 0, err
	}
	rb := make([]byte, 1500)
	for {
		n, peer, err := c.ReadFrom(rb)
		if err != nil {
			return 0, err
		}
		rtt := time.Since(start)
		if !sameIP(peer, ip) {
			continue
		}
		data := rb[:n]
		// macOS (both datagram and raw ICMP sockets) hands us the IPv4
		// header too. An ICMP echo reply starts with type 0, an IPv4 header
		// with 0x45, so this check is unambiguous.
		if n > 20 && data[0]>>4 == 4 {
			hl := int(data[0]&0x0f) * 4
			data = data[hl:]
		}
		rm, err := icmp.ParseMessage(1, data)
		if err != nil || rm.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		echo, ok := rm.Body.(*icmp.Echo)
		if !ok || echo.Seq != s || string(echo.Data) != string(nonce) {
			continue
		}
		return rtt, nil
	}
}

func sameIP(a net.Addr, ip net.IP) bool {
	switch v := a.(type) {
	case *net.UDPAddr:
		return v.IP.Equal(ip)
	case *net.IPAddr:
		return v.IP.Equal(ip)
	}
	return false
}

// TCPPinger measures TCP handshake time (≈ 1 RTT). Used when ICMP is not
// available. The connection is closed immediately.
type TCPPinger struct{ Port int }

func (p *TCPPinger) Mode() string { return fmt.Sprintf("tcp:%d", p.Port) }

func (p *TCPPinger) Ping(ctx context.Context, host string) (time.Duration, error) {
	var d net.Dialer
	start := time.Now()
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, fmt.Sprint(p.Port)))
	if err != nil {
		// A RST (connection refused) is still a full round trip.
		if errors.Is(err, syscall.ECONNREFUSED) {
			return time.Since(start), nil
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return 0, context.DeadlineExceeded
		}
		return 0, err
	}
	rtt := time.Since(start)
	_ = c.Close()
	return rtt, nil
}

// DNS measures how long the system's configured resolver takes to answer a
// single A query. Uses Go's resolver so that the OS cache (mDNSResponder)
// does not hide the network round trip.
type DNS struct {
	r *net.Resolver
}

func NewDNS() *DNS { return &DNS{r: &net.Resolver{PreferGo: true}} }

func (d *DNS) Lookup(ctx context.Context, name string) (time.Duration, error) {
	start := time.Now()
	_, err := d.r.LookupNetIP(ctx, "ip4", name)
	if err != nil {
		return 0, err
	}
	return time.Since(start), nil
}
