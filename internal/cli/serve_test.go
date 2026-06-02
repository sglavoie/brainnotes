package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	origBuild := runWebBuild
	origSourceRoot := sourceCheckoutRoot
	t.Cleanup(func() {
		runServe = origRun
		runWebBuild = origBuild
		sourceCheckoutRoot = origSourceRoot
	})
	sourceCheckoutRoot = ""
	runWebBuild = func(ctx context.Context, root string, out io.Writer) error {
		return nil
	}

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

func TestServe_BuildsWebAssetsInSourceCheckout(t *testing.T) {
	isolateBnEnv(t)
	projectRoot := buildServeWebProject(t)
	nested := filepath.Join(projectRoot, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	t.Chdir(nested)

	vaultRoot := buildOpenVault(t)
	cfgPath := writeServeTestConfig(t, vaultRoot, "[serve]\naddr = \"127.0.0.1:8800\"\nopen_browser = false")

	origRun := runServe
	origBuild := runWebBuild
	origSourceRoot := sourceCheckoutRoot
	t.Cleanup(func() {
		runServe = origRun
		runWebBuild = origBuild
		sourceCheckoutRoot = origSourceRoot
	})
	sourceCheckoutRoot = ""

	var buildRoot string
	runWebBuild = func(ctx context.Context, root string, out io.Writer) error {
		buildRoot = root
		return os.WriteFile(filepath.Join(root, "internal", "web", "static", "app.js"), []byte("fresh build"), 0o644)
	}

	var served bool
	runServe = func(ctx context.Context, cfg *config.Config, v *vault.Vault, opts web.Options) error {
		served = true
		if opts.StaticFiles == nil {
			t.Fatal("StaticFiles is nil, want disk static assets")
		}
		f, err := opts.StaticFiles.Open("app.js")
		if err != nil {
			t.Fatalf("open app.js: %v", err)
		}
		defer f.Close()
		body, err := io.ReadAll(f)
		if err != nil {
			t.Fatalf("read app.js: %v", err)
		}
		if string(body) != "fresh build" {
			t.Fatalf("app.js = %q, want fresh build", string(body))
		}
		return nil
	}

	if _, _, err := runServeCmd(t, "--config", cfgPath, "serve"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if buildRoot != projectRoot {
		t.Fatalf("build root = %q, want %q", buildRoot, projectRoot)
	}
	if !served {
		t.Fatal("runServe was not called")
	}
}

func TestServe_BuildsWebAssetsFromStampedCheckoutOutsideCheckout(t *testing.T) {
	isolateBnEnv(t)
	projectRoot := buildServeWebProject(t)
	t.Chdir(t.TempDir())

	vaultRoot := buildOpenVault(t)
	cfgPath := writeServeTestConfig(t, vaultRoot, "[serve]\naddr = \"127.0.0.1:8800\"\nopen_browser = false")

	origRun := runServe
	origBuild := runWebBuild
	origSourceRoot := sourceCheckoutRoot
	t.Cleanup(func() {
		runServe = origRun
		runWebBuild = origBuild
		sourceCheckoutRoot = origSourceRoot
	})
	sourceCheckoutRoot = projectRoot

	var buildRoot string
	runWebBuild = func(ctx context.Context, root string, out io.Writer) error {
		buildRoot = root
		return os.WriteFile(filepath.Join(root, "internal", "web", "static", "app.js"), []byte("fresh build"), 0o644)
	}

	runServe = func(ctx context.Context, cfg *config.Config, v *vault.Vault, opts web.Options) error {
		if opts.StaticFiles == nil {
			t.Fatal("StaticFiles is nil, want disk static assets")
		}
		f, err := opts.StaticFiles.Open("app.js")
		if err != nil {
			t.Fatalf("open app.js: %v", err)
		}
		defer f.Close()
		body, err := io.ReadAll(f)
		if err != nil {
			t.Fatalf("read app.js: %v", err)
		}
		if string(body) != "fresh build" {
			t.Fatalf("app.js = %q, want fresh build", string(body))
		}
		return nil
	}

	if _, _, err := runServeCmd(t, "--config", cfgPath, "serve"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if buildRoot != projectRoot {
		t.Fatalf("build root = %q, want %q", buildRoot, projectRoot)
	}
}

func TestServe_SkipsWebBuildOutsideSourceCheckout(t *testing.T) {
	isolateBnEnv(t)
	t.Chdir(t.TempDir())

	vaultRoot := buildOpenVault(t)
	cfgPath := writeServeTestConfig(t, vaultRoot, "[serve]\naddr = \"127.0.0.1:8800\"\nopen_browser = false")

	origRun := runServe
	origBuild := runWebBuild
	origSourceRoot := sourceCheckoutRoot
	t.Cleanup(func() {
		runServe = origRun
		runWebBuild = origBuild
		sourceCheckoutRoot = origSourceRoot
	})
	sourceCheckoutRoot = ""

	var buildCalled bool
	runWebBuild = func(ctx context.Context, root string, out io.Writer) error {
		buildCalled = true
		return nil
	}

	runServe = func(ctx context.Context, cfg *config.Config, v *vault.Vault, opts web.Options) error {
		if opts.StaticFiles != nil {
			t.Fatal("StaticFiles is set, want embedded assets outside source checkout")
		}
		return nil
	}

	if _, _, err := runServeCmd(t, "--config", cfgPath, "serve"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if buildCalled {
		t.Fatal("runWebBuild was called outside a source checkout")
	}
}

func TestServe_WebBuildFailureStopsBeforeServing(t *testing.T) {
	isolateBnEnv(t)
	projectRoot := buildServeWebProject(t)
	t.Chdir(projectRoot)

	vaultRoot := buildOpenVault(t)
	cfgPath := writeServeTestConfig(t, vaultRoot, "[serve]\naddr = \"127.0.0.1:8800\"\nopen_browser = false")

	origRun := runServe
	origBuild := runWebBuild
	origSourceRoot := sourceCheckoutRoot
	t.Cleanup(func() {
		runServe = origRun
		runWebBuild = origBuild
		sourceCheckoutRoot = origSourceRoot
	})
	sourceCheckoutRoot = ""

	runWebBuild = func(ctx context.Context, root string, out io.Writer) error {
		return errors.New("npm failed")
	}

	var served bool
	runServe = func(ctx context.Context, cfg *config.Config, v *vault.Vault, opts web.Options) error {
		served = true
		return nil
	}

	_, _, err := runServeCmd(t, "--config", cfgPath, "serve")
	if err == nil {
		t.Fatal("execute succeeded, want build error")
	}
	if !strings.Contains(err.Error(), "serve: build web assets") {
		t.Fatalf("error = %q, want build failure context", err)
	}
	if served {
		t.Fatal("runServe was called after build failure")
	}
}

func TestRunWebBuildPrefersLocalEsbuildBinary(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	esbuildPath := filepath.Join(root, "node_modules", "esbuild", "bin", "esbuild")
	if err := os.MkdirAll(filepath.Dir(esbuildPath), 0o755); err != nil {
		t.Fatalf("mkdir esbuild bin: %v", err)
	}
	argsPath := filepath.Join(root, "args.txt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > args.txt\n"
	if err := os.WriteFile(esbuildPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write esbuild script: %v", err)
	}

	var out bytes.Buffer
	if err := runWebBuild(context.Background(), root, &out); err != nil {
		t.Fatalf("runWebBuild: %v\n%s", err, out.String())
	}

	if strings.Contains(out.String(), "npm run build:web") {
		t.Fatalf("runWebBuild fell back to npm despite local esbuild: %q", out.String())
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	want := strings.Join(webBuildArgs(), "\n") + "\n"
	if string(args) != want {
		t.Fatalf("esbuild args = %q, want %q", string(args), want)
	}
}

func buildServeWebProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	writeFile("package.json", `{"scripts":{"build:web":"echo ok"}}`)
	writeFile("internal/web/static_src/main.ts", "console.log('source');")
	writeFile("internal/web/static/app.js", "stale build")
	return root
}

func writeServeTestConfig(t *testing.T, vaultRoot, serveBlock string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	body := "vault = \"" + vaultRoot + "\"\n" + serveBlock + "\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}
