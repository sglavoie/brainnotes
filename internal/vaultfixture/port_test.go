package vaultfixture

import (
	"net"
	"testing"
)

// TestListenLoopbackIsLoopbackOnlyAndUnique states both halves of port
// isolation: a fixture server is not reachable from the network, and two
// fixtures alive at once never share a port.
func TestListenLoopbackIsLoopbackOnlyAndUnique(t *testing.T) {
	first := ListenLoopback(t)
	second := ListenLoopback(t)

	for _, listener := range []net.Listener{first, second} {
		addr, ok := listener.Addr().(*net.TCPAddr)
		if !ok {
			t.Fatalf("listener address %q is not TCP", listener.Addr())
		}
		if !addr.IP.IsLoopback() {
			t.Fatalf("listener bound %q, which is reachable from the network", addr)
		}
		if addr.Port == 0 {
			t.Fatal("listener reported port 0, so nothing is actually bound")
		}
	}
	if first.Addr().String() == second.Addr().String() {
		t.Fatalf("two concurrent fixtures share %q", first.Addr())
	}
}

// TestReserveLoopbackAddrIsFreeAndLoopback covers the child-process case: the
// address handed to a server that must be told where to bind is loopback, and
// binding it succeeds because the reservation released it.
func TestReserveLoopbackAddrIsFreeAndLoopback(t *testing.T) {
	addr := ReserveLoopbackAddr(t)

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		t.Fatalf("ReserveLoopbackAddr returned %q, which is not on the loopback interface", addr)
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the reserved address %q is not free: %v", addr, err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// TestRequireLoopbackRefusesAWildcardBind keeps the guard from being vacuous:
// the address a careless fixture would produce is refused.
func TestRequireLoopbackRefusesAWildcardBind(t *testing.T) {
	err := requireLoopback(&net.TCPAddr{IP: net.IPv4zero, Port: 8080})
	if err == nil {
		t.Fatal("requireLoopback admitted 0.0.0.0:8080, which is reachable from the network")
	}
	if err := requireLoopback(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}); err != nil {
		t.Fatalf("requireLoopback refused a genuine loopback address: %v", err)
	}
}
