package facade

import (
	"net/netip"
	"time"

	"nightclip/core/notes"
	"nightclip/core/pairing"
	"nightclip/core/peer"
	"nightclip/core/vsync"
	"nightclip/core/xfer"
)

// Every Core built by these tests uses loopback and random ports: the tests must
// never bind (or dial) the real tailnet addresses of the machine they run on.
func init() {
	testEngine = &struct {
		hooks  peer.Hooks
		timing peer.Timing
		pairT  pairing.Timings
		xferT  xfer.Timing
		syncT  vsync.Timing
		notesT notes.Timing
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
		pairT:  pairing.Timings{RateBurst: 1000},
		xferT:  xfer.Timing{ProgressEvery: time.Millisecond, RetryBase: 20 * time.Millisecond, RetryMax: 100 * time.Millisecond},
		syncT:  vsync.Timing{Poll: 150 * time.Millisecond, HTTP: 5 * time.Second, MinPutGap: 20 * time.Millisecond},
		notesT: notes.Timing{Poll: 150 * time.Millisecond, HTTP: 5 * time.Second},
	}
}
