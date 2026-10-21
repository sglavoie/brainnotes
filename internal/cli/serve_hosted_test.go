package cli

import (
	"context"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/web"
)

// TestServe_HostedProfile covers the flag that publishes the vault itself. The
// three properties it asserts are the ones a hosted deployment would be broken
// without: the option reaches the server, the browser is not opened on the
// serving machine, and the static assets are still prepared — hosted is not
// read-only, and a hosted server with no bundle serves an unstyled page.
func TestServe_HostedProfile(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)
	writeServeTestConfig(t, vaultRoot, "[serve]\naddr = \"127.0.0.1:8800\"\nopen_browser = true\n")

	origRun := runServe
	origBuild := runWebBuild
	origSourceRoot := sourceCheckoutRoot
	t.Cleanup(func() {
		runServe = origRun
		runWebBuild = origBuild
		sourceCheckoutRoot = origSourceRoot
	})
	sourceCheckoutRoot = buildServeWebProject(t)
	built := false
	runWebBuild = func(ctx context.Context, root string, out io.Writer) error {
		built = true
		return nil
	}

	var gotOpts web.Options
	var gotServe config.ServeConfig
	var gotStatic fs.FS
	runServe = func(ctx context.Context, cfg *config.Config, v *vault.Vault, opts web.Options) error {
		gotOpts = opts
		gotServe = cfg.Serve
		gotStatic = opts.StaticFiles
		return nil
	}

	if _, _, err := runServeCmd(t, "--vault", vaultRoot, "serve", "--hosted"); err != nil {
		t.Fatalf("serve --hosted: %v", err)
	}
	if !gotOpts.Hosted || gotOpts.ReadOnly {
		t.Fatalf("options = %+v, want Hosted only", gotOpts)
	}
	if gotServe.BrowserEnabled() {
		t.Fatal("hosted mode opened a browser on the serving machine")
	}
	if !built || gotStatic == nil {
		t.Fatalf("hosted mode skipped the web asset build: built=%v static=%v", built, gotStatic != nil)
	}
}

// TestServe_RefusesBothPublishedProfiles pins the mutually-exclusive pair at the
// CLI. They are different deployments of different vaults, so asking for both is
// a question rather than something to resolve silently.
func TestServe_RefusesBothPublishedProfiles(t *testing.T) {
	isolateBnEnv(t)
	vaultRoot := buildOpenVault(t)
	writeServeTestConfig(t, vaultRoot, "[serve]\naddr = \"127.0.0.1:8800\"\nopen_browser = false\n")

	origRun := runServe
	t.Cleanup(func() { runServe = origRun })
	runServe = func(ctx context.Context, cfg *config.Config, v *vault.Vault, opts web.Options) error {
		t.Fatal("server started with two serving profiles")
		return nil
	}

	_, _, err := runServeCmd(t, "--vault", vaultRoot, "serve", "--hosted", "--read-only", "--state-dir", t.TempDir())
	if err == nil {
		t.Fatal("accepted --hosted with --read-only")
	}
	if !strings.Contains(err.Error(), "read-only") || !strings.Contains(err.Error(), "hosted") {
		t.Fatalf("error = %q, want it to name both flags", err)
	}
}
