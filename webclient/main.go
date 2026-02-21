package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	webclientapp "github.com/bhandras/delight/webclient/internal"
)

const (
	// defaultListenAddr is the local HTTP address used by the bridge.
	defaultListenAddr = "127.0.0.1:4411"
	// defaultServerURL is the default Delight API base URL.
	defaultServerURL = "http://127.0.0.1:3005"
	// shutdownTimeout bounds graceful HTTP shutdown time.
	shutdownTimeout = 5 * time.Second
)

// main starts the web bridge process.
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run parses flags, initializes the bridge app, and serves HTTP until signaled.
func run() error {
	var cfg webclientapp.Config
	flag.StringVar(&cfg.ListenAddr, "listen", defaultListenAddr, "bridge listen address")
	flag.StringVar(&cfg.ServerURL, "server-url", defaultServerURL, "Delight server URL")
	flag.StringVar(&cfg.StatePath, "state-path", "", "runtime state file path")
	flag.StringVar(&cfg.APIToken, "api-token", strings.TrimSpace(os.Getenv("DELIGHT_WEBCLIENT_API_TOKEN")), "optional API bearer token")
	flag.StringVar(&cfg.OriginsCSV, "origins", strings.TrimSpace(os.Getenv("DELIGHT_WEBCLIENT_ORIGINS")), "comma-separated Origin allowlist")
	flag.Parse()

	if err := validateNetworkSecurity(cfg.ListenAddr, cfg.APIToken, cfg.OriginsCSV); err != nil {
		return err
	}

	app, err := webclientapp.NewApp(cfg)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.Serve()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return app.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// validateNetworkSecurity enforces required auth/origin settings for non-loopback binds.
func validateNetworkSecurity(listenAddr, token, originsCSV string) error {
	host := listenAddr
	if parsedHost, _, err := net.SplitHostPort(listenAddr); err == nil {
		host = parsedHost
	}
	if isLoopbackHost(host) {
		return nil
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("non-loopback listen requires --api-token")
	}
	if strings.TrimSpace(originsCSV) == "" {
		return fmt.Errorf("non-loopback listen requires --origins allowlist")
	}
	return nil
}

// isLoopbackHost reports whether host represents loopback-only binding.
func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
