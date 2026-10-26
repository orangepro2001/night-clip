package facade

import (
	"net/netip"
	"time"

	"nightclip/core/pairing"
	"nightclip/core/peer"
)

// Every Core built by these tests uses loopback and random ports: the tests must
// never bind (or dial) the real tailnet addresses of the machine they run on.
func init() {
	testEngine = &struct {
		hooks  peer.Hooks
		timing peer.Timing
		pairT  pairing.Timings
	}{
		hooks: peer.Hooks{
			AllowLoopback: true, RandomPort: true,
			ListenAddrs: func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("127.0.0.1")} },
		},
		timing: peer.Timing{
			Ping: 100 * time.Millisecond, Idle: 700 * time.Millisecond, Handshake: 2 * time.Second,
			DialTimeout: time.Second, BackoffBase: 40 * time.Millisecond, BackoffMax: 320 * time.Millisecond,
			StableAfter: 200 * time.Millisecond, Rebind: 30 * time.Millisecond,
		},
		pairT: pairing.Timings{RateBurst: 1000},
	}
}
