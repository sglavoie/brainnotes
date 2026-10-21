package cli

import (
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
)

// TestResolveServeConfig pins how --addr and --port compose over [serve].addr:
// --addr replaces the address, and --port then rewrites only the port of
// whichever address won, so giving both keeps both.
func TestResolveServeConfig(t *testing.T) {
	cases := []struct {
		name        string
		base        string
		addr        string
		addrChanged bool
		port        int
		portChanged bool
		want        string
		wantErr     string
	}{
		{name: "config only", base: "127.0.0.1:8800", want: "127.0.0.1:8800"},
		{name: "default when config empty", base: "", want: config.DefaultServeAddr},
		{name: "port rewrites config port", base: "127.0.0.1:8800", port: 9911, portChanged: true, want: "127.0.0.1:9911"},
		{name: "addr replaces config", base: "127.0.0.1:8800", addr: "0.0.0.0:7000", addrChanged: true, want: "0.0.0.0:7000"},
		{name: "port rewrites addr port", base: "127.0.0.1:8800", addr: "0.0.0.0:7000", addrChanged: true, port: 9911, portChanged: true, want: "0.0.0.0:9911"},
		{name: "port zero is legal", base: "127.0.0.1:8800", port: 0, portChanged: true, want: "127.0.0.1:0"},
		{name: "ipv6 addr keeps brackets", base: "127.0.0.1:8800", addr: "[::1]:7000", addrChanged: true, port: 9911, portChanged: true, want: "[::1]:9911"},
		{name: "empty addr", base: "127.0.0.1:8800", addr: "  ", addrChanged: true, wantErr: "serve: --addr cannot be empty"},
		{name: "addr without port", base: "127.0.0.1:8800", addr: "localhost", addrChanged: true, wantErr: `serve: invalid --addr "localhost": address localhost: missing port in address`},
		{name: "port too large", base: "127.0.0.1:8800", port: 70000, portChanged: true, wantErr: "serve: --port must be between 0 and 65535 (got 70000)"},
		{name: "negative port", base: "127.0.0.1:8800", port: -1, portChanged: true, wantErr: "serve: --port must be between 0 and 65535 (got -1)"},
		{name: "port range checked before addr", base: "127.0.0.1:8800", addr: "localhost", addrChanged: true, port: 70000, portChanged: true, wantErr: "serve: --port must be between 0 and 65535 (got 70000)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveServeConfig(config.ServeConfig{Addr: tc.base}, tc.addr, tc.addrChanged, tc.port, tc.portChanged, false)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Addr != tc.want {
				t.Fatalf("addr = %q, want %q", got.Addr, tc.want)
			}
		})
	}
}
