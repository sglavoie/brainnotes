package vaultfixture

import (
	"fmt"
	"net"
	"testing"
)

// LoopbackHost is the only interface a fixture server may bind. A fixture that
// bound 0.0.0.0 would be reachable from the network for as long as the test
// ran, and on a laptop that is the same network the real services are on.
const LoopbackHost = "127.0.0.1"

// ListenLoopback opens a listener on an operating-system-chosen port of the
// loopback interface and closes it when the test ends.
//
// Port zero rather than a constant is the isolation: two fixtures running at
// once never contend, and a fixture never inherits a port a previous run left
// something listening on — which would make a test pass against the wrong
// server.
func ListenLoopback(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(LoopbackHost, "0"))
	if err != nil {
		t.Fatalf("listen on %s:0: %v", LoopbackHost, err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := requireLoopback(listener.Addr()); err != nil {
		t.Fatalf("refusing a fixture listener: %v", err)
	}
	return listener
}

// ReserveLoopbackAddr returns a host:port on loopback that was free a moment
// ago, having closed the listener that proved it.
//
// The gap between closing and binding is a real race, so prefer ListenLoopback
// and hand the listener to the server. This exists for the case that cannot use
// one: a server started as a child process, which must be told an address
// before it can bind anything.
func ReserveLoopbackAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(LoopbackHost, "0"))
	if err != nil {
		t.Fatalf("reserve a port on %s: %v", LoopbackHost, err)
	}
	addr := listener.Addr()
	if err := listener.Close(); err != nil {
		t.Fatalf("release the reserved port: %v", err)
	}
	if err := requireLoopback(addr); err != nil {
		t.Fatalf("refusing a reserved fixture address: %v", err)
	}
	return addr.String()
}

// requireLoopback refuses an address that is not on the loopback interface.
func requireLoopback(addr net.Addr) error {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("%q is not a TCP address, so it cannot be proved to be loopback-only", addr)
	}
	if !tcp.IP.IsLoopback() {
		return fmt.Errorf("%q is not on the loopback interface, so a fixture server bound to it would be reachable from the network", addr)
	}
	return nil
}
