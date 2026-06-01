package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/web"
)

var runServe = func(ctx context.Context, cfg *config.Config, v *vault.Vault, opts web.Options) error {
	srv, err := web.NewServer(cfg, v, opts)
	if err != nil {
		return err
	}
	return srv.Run(ctx)
}

func NewServeCmd() *cobra.Command {
	var (
		addr   string
		port   int
		noOpen bool
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
				noOpen,
			)
			if err != nil {
				return err
			}

			cfgCopy := *cfg
			cfgCopy.Serve = serveCfg

			v, err := vault.Open(&cfgCopy)
			if err != nil {
				return err
			}

			opts := web.Options{
				NotifyListening: func(url string) {
					fmt.Fprintf(cmd.ErrOrStderr(), "serving at %s\n", url)
				},
			}

			return runServe(cmd.Context(), &cfgCopy, v, opts)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "", "listen address host:port (overrides [serve].addr)")
	cmd.Flags().IntVar(&port, "port", 0, "listen port (overrides the port from [serve].addr)")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "do not open a browser on startup")

	return cmd
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
