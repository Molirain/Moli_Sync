// Command collector receives anonymous aggregate reports from MoliSync server
// deployments and exposes a global summary.
//
// Deploy it behind TLS (directly or via a reverse proxy) on a host you control:
//
//	MOLISYNC_COLLECTOR_ADMIN_TOKEN=... ./collector
//
// Every deployment then sets telemetry.endpoint to
// https://<collector>/v1/report and optionally the same report token.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/molirain/molisync/server/internal/collector"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("collector stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	printVersion := flag.Bool("version", false, "print the version and exit")
	healthcheckURL := flag.String("healthcheck-url", "", "probe a running collector's /healthz endpoint and exit")
	flag.Parse()

	if *printVersion {
		fmt.Println(version)
		return nil
	}

	if *healthcheckURL != "" {
		return healthcheck(*healthcheckURL)
	}

	listen := env("MOLISYNC_COLLECTOR_LISTEN", "127.0.0.1:9530")
	dataDir := env("MOLISYNC_COLLECTOR_DATA_DIR", "./collector-data")
	adminToken := strings.TrimSpace(os.Getenv("MOLISYNC_COLLECTOR_ADMIN_TOKEN"))
	reportToken := strings.TrimSpace(os.Getenv("MOLISYNC_COLLECTOR_TOKEN"))
	rateLimit := envInt("MOLISYNC_COLLECTOR_RATE_PER_MINUTE", 12)
	shutdownTimeout := envDuration("MOLISYNC_COLLECTOR_SHUTDOWN_TIMEOUT", 10*time.Second)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if adminToken == "" {
		logger.Warn("MOLISYNC_COLLECTOR_ADMIN_TOKEN is empty; /v1/stats stays disabled")
	}
	if len(adminToken) > 0 && len(adminToken) < 16 {
		return errors.New("MOLISYNC_COLLECTOR_ADMIN_TOKEN must be at least 16 characters")
	}
	if len(reportToken) > 0 && len(reportToken) < 16 {
		return errors.New("MOLISYNC_COLLECTOR_TOKEN must be at least 16 characters")
	}

	store, err := collector.Open(dataDir)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			logger.Error("close collector store", "error", closeErr)
		}
	}()

	handler := collector.NewHandler(store, collector.HandlerOptions{
		ReportToken:   reportToken,
		AdminToken:    adminToken,
		RatePerMinute: rateLimit,
		Logger:        logger,
	})

	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", listen, err)
	}

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()

	logger.Info("MoliSync collector listening",
		"version", version,
		"address", listen,
		"stats_enabled", adminToken != "",
		"report_token_required", reportToken != "",
	)

	select {
	case <-ctx.Done():
	case serveErr := <-serveErrors:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return server.Shutdown(shutdownContext)
}

func healthcheck(rawURL string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(rawURL)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: unexpected status %d", response.StatusCode)
	}
	return nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
