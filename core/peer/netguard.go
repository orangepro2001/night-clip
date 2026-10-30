// Package peer is the device-to-device layer: mutual-TLS listener and dialer,
// the WebSocket link per paired device, the outbox/inbox protocol with
// per-link sequences and cursors (docs/architecture.md §3.3, §4).
package peer

import (
	"net"
	"net/netip"
	"sort"
)

var (
	tailnetV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// InTailnet reports whether a is a Tailscale address (100.64.0.0/10 or
// fd7a:115c:a1e0::/48). IPv4-mapped IPv6 forms are unmapped first.
func InTailnet(a netip.Addr) bool {
	a = a.Unmap()
	return tailnetV4.Contains(a) || tailnetV6.Contains(a)
}

// TailnetAddrs lists this machine's tailnet addresses. Only a listening
// (Windows) core calls it: enumerating interfaces is restricted on Android,
// where the core only dials out.
func TailnetAddrs() []netip.Addr {
	out, _ := tailnetAddrs()
	return out
}

func tailnetAddrs() ([]netip.Addr, error) {
	all, err := LocalAddrs()
	var out []netip.Addr
	for _, a := range all {
		if InTailnet(a) {
			out = append(out, a)
		}
	}
	return out, err
}

// LocalAddrs lists every address on this machine's interfaces, sorted
// (diagnostics show it, so a missing tailnet address can be told apart from
// one that is there but not recognised).
func LocalAddrs() ([]netip.Addr, error) {
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var out []netip.Addr
	for _, ia := range ifaces {
		ipn, ok := ia.(*net.IPNet)
		if !ok {
			continue
		}
		if a, ok := netip.AddrFromSlice(ipn.IP); ok {
			out = append(out, a.Unmap())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out, nil
}

// tailnetStrings returns the tailnet addresses among in, as strings, dropping
// everything else (a peer must not be able to make us dial arbitrary hosts).
func (e *Engine) tailnetStrings(in []string) []string {
	var out []string
	for _, s := range in {
		if a, err := netip.ParseAddr(s); err == nil && e.addrAllowed(a) {
			out = append(out, a.Unmap().String())
		}
	}
	return out
}

// addrAllowed is InTailnet, plus loopback when the engine runs in test mode.
func (e *Engine) addrAllowed(a netip.Addr) bool {
	return InTailnet(a) || (e.hooks.AllowLoopback && a.Unmap().IsLoopback())
}

// AddrAllowed reports whether a is acceptable as a peer address: a tailnet
// address (or loopback in tests). Pairing uses it to vet hosts from QR codes.
func (e *Engine) AddrAllowed(a netip.Addr) bool { return e.addrAllowed(a) }
