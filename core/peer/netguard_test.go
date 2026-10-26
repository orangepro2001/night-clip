package peer

import (
	"net/netip"
	"testing"
)

func TestInTailnet(t *testing.T) {
	yes := []string{"100.64.0.1", "100.101.146.117", "100.127.255.254", "fd7a:115c:a1e0::1", "fd7a:115c:a1e0:ab12::5", "::ffff:100.101.146.117"}
	no := []string{"100.63.255.255", "100.128.0.0", "127.0.0.1", "10.0.0.1", "192.168.1.5", "8.8.8.8", "::1", "fd7a:115c:a1e1::1", "fe80::1", "0.0.0.0"}
	for _, s := range yes {
		if !InTailnet(netip.MustParseAddr(s)) {
			t.Errorf("%s must be a tailnet address", s)
		}
	}
	for _, s := range no {
		if InTailnet(netip.MustParseAddr(s)) {
			t.Errorf("%s must not be a tailnet address", s)
		}
	}
}

func TestEngineAddressFilteringHonoursTestHookOnly(t *testing.T) {
	prod := &Engine{}
	if got := prod.tailnetStrings([]string{"100.64.0.9", "127.0.0.1", "8.8.8.8", "junk", "fd7a:115c:a1e0::2"}); len(got) != 2 || got[0] != "100.64.0.9" {
		t.Fatalf("production filter = %v", got)
	}
	test := &Engine{hooks: Hooks{AllowLoopback: true}}
	if got := test.tailnetStrings([]string{"127.0.0.1", "8.8.8.8"}); len(got) != 1 || got[0] != "127.0.0.1" {
		t.Fatalf("test-mode filter = %v", got)
	}
}
