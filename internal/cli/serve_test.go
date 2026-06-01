package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/web"
)

func runServeCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := NewRootCmd()
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errBuf)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func TestServe_FlagPrecedence(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)
	cfgPath := filepath.Join(t.TempDir(), "config.toml")

	origRun := runServe
	t.Cleanup(func() { runServe = origRun })

	var got config.ServeConfig
	runServe = func(ctx context.Context, cfg *config.Config, v *vault.Vault, opts web.Options) error {
		got = cfg.Serve
		return nil
	}

	writeCfg := func(t *testing.T, serveBlock string) {
		t.Helper()
		body := "vault = \"" + vaultRoot + "\"\n" + serveBlock + "\n"
		if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}

	t.Run("uses config when flags unset", func(t *testing.T) {
		writeCfg(t, "[serve]\naddr = \"127.0.0.1:8800\"\nopen_browser = false")
		if _, _, err := runServeCmd(t, "--config", cfgPath, "serve"); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if got.Addr != "127.0.0.1:8800" {
			t.Fatalf("addr: got %q, want %q", got.Addr, "127.0.0.1:8800")
		}
		if got.BrowserEnabled() {
			t.Fatal("BrowserEnabled: got true, want false")
		}
	})

	t.Run("port overlays config host", func(t *testing.T) {
		writeCfg(t, "[serve]\naddr = \"127.0.0.1:8800\"")
		if _, _, err := runServeCmd(t, "--config", cfgPath, "serve", "--port", "9911"); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if got.Addr != "127.0.0.1:9911" {
			t.Fatalf("addr: got %q, want %q", got.Addr, "127.0.0.1:9911")
		}
	})

	t.Run("addr overrides port", func(t *testing.T) {
		writeCfg(t, "[serve]\naddr = \"127.0.0.1:8800\"")
		if _, _, err := runServeCmd(t, "--config", cfgPath, "serve", "--port", "9911", "--addr", "0.0.0.0:7000"); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if got.Addr != "0.0.0.0:7000" {
			t.Fatalf("addr: got %q, want %q", got.Addr, "0.0.0.0:7000")
		}
	})

	t.Run("no-open forces browser disabled", func(t *testing.T) {
		writeCfg(t, "[serve]\naddr = \"127.0.0.1:8800\"\nopen_browser = true")
		if _, _, err := runServeCmd(t, "--config", cfgPath, "serve", "--no-open"); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if got.BrowserEnabled() {
			t.Fatal("BrowserEnabled: got true, want false")
		}
	})
}
