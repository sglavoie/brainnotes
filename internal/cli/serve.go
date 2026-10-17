package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/web"
	"github.com/sglavoie/sglavoie_brainnotes/internal/web/cssbuild"
)

var runServe = func(ctx context.Context, cfg *config.Config, v *vault.Vault, opts web.Options) error {
	srv, err := web.NewServer(cfg, v, opts)
	if err != nil {
		return err
	}
	return srv.Run(ctx)
}

var runWebBuild = func(ctx context.Context, root string, out io.Writer) error {
	fmt.Fprintln(out, "building web assets...")
	if bin, ok := localEsbuildBinary(root); ok {
		cmd := exec.CommandContext(ctx, bin, webBuildArgs()...)
		cmd.Dir = root
		cmd.Stdout = out
		cmd.Stderr = out
		return cmd.Run()
	}

	fmt.Fprintln(out, "local esbuild binary not found; falling back to npm run build:web")
	cmd := exec.CommandContext(ctx, "npm", "run", "build:web")
	cmd.Dir = root
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(`local esbuild binary not found and npm run build:web failed; run "npm ci" in %s to install web dependencies: %w`, root, err)
	}
	return nil
}

func localEsbuildBinary(root string) (string, bool) {
	bin := filepath.Join(root, "node_modules", "esbuild", "bin", "esbuild")
	info, err := os.Stat(bin)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return "", false
	}
	return bin, true
}

func webBuildArgs() []string {
	return []string{
		"app=internal/web/static_src/main.ts",
		"editing=internal/web/static_src/editing.ts",
		"--bundle",
		"--minify",
		"--format=iife",
		"--global-name=BrainNotesApp",
		"--outdir=internal/web/static",
	}
}

// sourceCheckoutRoot is stamped by local just build/install recipes so a
// developer binary can rebuild web assets even when bn is launched elsewhere.
var sourceCheckoutRoot string

func NewServeCmd() *cobra.Command {
	var (
		addr              string
		port              int
		noOpen            bool
		readOnly          bool
		hosted            bool
		stateDir          string
		replicaStatusFile string
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve a local web view of your vault",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("serve: missing config in command context")
			}

			serveCfg, err := resolveServeConfig(
				cfg.Serve,
				addr,
				cmd.Flags().Changed("addr"),
				port,
				cmd.Flags().Changed("port"),
				// Neither published profile opens a browser: the machine running
				// the process is not the machine the person is reading from.
				noOpen || readOnly || hosted,
			)
			if err != nil {
				return err
			}

			var staticFiles fs.FS
			if !readOnly {
				staticFiles, err = prepareServeStaticFiles(cmd.Context(), cmd.ErrOrStderr())
			}
			if err != nil {
				return err
			}

			cfgCopy := *cfg
			cfgCopy.Serve = serveCfg

			openVault := vault.Open
			if readOnly {
				openVault = vault.OpenReadOnly
			}
			v, err := openVault(&cfgCopy)
			if err != nil {
				return err
			}

			opts := web.Options{
				ReadOnly:          readOnly,
				Hosted:            hosted,
				StateDir:          stateDir,
				ReplicaStatusFile: replicaStatusFile,
				NotifyListening: func(url string) {
					fmt.Fprintf(cmd.ErrOrStderr(), "serving at %s\n", url)
				},
				StaticFiles: staticFiles,
			}

			return runServe(cmd.Context(), &cfgCopy, v, opts)
		},
	}

	cmd.Flags().BoolVar(&readOnly, "read-only", false, "serve a read-only replica with external activity state")
	cmd.Flags().BoolVar(&hosted, "hosted", false, "serve this vault over the network: document actions stay available, process launches are denied")
	// The two published profiles are different deployments of different vaults,
	// so asking for both is a question rather than something to resolve. The
	// library boundary refuses the same pair in web.modeFor; this is only the
	// clearer message for a person at a shell.
	cmd.MarkFlagsMutuallyExclusive("read-only", "hosted")
	cmd.Flags().StringVar(&stateDir, "state-dir", "", "external directory for Daily Brief activity (required with --read-only)")
	cmd.Flags().StringVar(&replicaStatusFile, "replica-status-file", "", "replica synchronization status JSON")
	cmd.Flags().StringVar(&addr, "addr", "", "listen address host:port (overrides [serve].addr)")
	cmd.Flags().IntVar(&port, "port", 0, "listen port (overrides the port from [serve].addr)")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "do not open a browser on startup")

	return cmd
}

func prepareServeStaticFiles(ctx context.Context, out io.Writer) (fs.FS, error) {
	root, ok, err := serveWebProjectRoot()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	// The stylesheet is generated too, but by internal/web/cssbuild rather than
	// esbuild, so it is regenerated here even in a checkout with no Node
	// toolchain installed. It runs first because it cannot fail for a reason the
	// JavaScript build would explain better.
	if _, err := cssbuild.WriteBundle(root); err != nil {
		return nil, fmt.Errorf("serve: build web assets: %w", err)
	}

	if err := runWebBuild(ctx, root, out); err != nil {
		return nil, fmt.Errorf("serve: build web assets: %w", err)
	}
	return os.DirFS(filepath.Join(root, "internal", "web", "static")), nil
}

func serveWebProjectRoot() (string, bool, error) {
	if root, ok := validateWebProjectRoot(sourceCheckoutRoot); ok {
		return root, true, nil
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", false, fmt.Errorf("serve: determine working directory: %w", err)
	}
	root, ok := findWebProjectRoot(wd)
	return root, ok, nil
}

func findWebProjectRoot(start string) (string, bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		if _, ok := validateWebProjectRoot(dir); ok {
			return dir, true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func validateWebProjectRoot(root string) (string, bool) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", false
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	if fileExists(filepath.Join(abs, "package.json")) &&
		fileExists(filepath.Join(abs, "internal", "web", "static_src", "main.ts")) {
		return abs, true
	}
	return "", false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func resolveServeConfig(base config.ServeConfig, addrFlag string, addrChanged bool, port int, portChanged bool, noOpen bool) (config.ServeConfig, error) {
	out := base
	out.Addr = strings.TrimSpace(out.Addr)
	if out.Addr == "" {
		out.Addr = config.DefaultServeAddr
	}

	if addrChanged {
		out.Addr = strings.TrimSpace(addrFlag)
		if out.Addr == "" {
			return config.ServeConfig{}, errors.New("serve: --addr cannot be empty")
		}
	} else if portChanged {
		addr, err := replaceAddressPort(out.Addr, port)
		if err != nil {
			return config.ServeConfig{}, err
		}
		out.Addr = addr
	}

	if noOpen {
		disabled := false
		out.OpenBrowser = &disabled
	}

	return out, nil
}

func replaceAddressPort(addr string, port int) (string, error) {
	if port < 0 || port > 65535 {
		return "", fmt.Errorf("serve: --port must be between 0 and 65535, got %d", port)
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("serve: invalid address %q: %w", addr, err)
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}
