// Command server runs the MoliSync control plane.
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
	"strings"
	"syscall"
	"time"

	"github.com/molirain/molisync/server/internal/api"
	"github.com/molirain/molisync/server/internal/auth"
	"github.com/molirain/molisync/server/internal/config"
	"github.com/molirain/molisync/server/internal/p2p"
	"github.com/molirain/molisync/server/internal/registry"
	"github.com/molirain/molisync/server/internal/store"
	"github.com/molirain/molisync/server/internal/telemetry"
	"github.com/molirain/molisync/server/internal/updatecheck"
	"github.com/molirain/molisync/server/internal/ws"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "config.yaml", "path to the YAML configuration file")
	printVersion := flag.Bool("version", false, "print the version and exit")
	healthcheckURL := flag.String("healthcheck-url", "", "probe a running instance's /healthz endpoint and exit")
	flag.Parse()

	if *printVersion {
		fmt.Println(version)
		return nil
	}

	if *healthcheckURL != "" {
		return healthcheck(*healthcheckURL)
	}

	explicitConfig := false
	flag.Visit(func(visited *flag.Flag) {
		if visited.Name == "config" {
			explicitConfig = true
		}
	})

	path := *configPath
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			if explicitConfig || !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("configuration file %q: %w", path, err)
			}
			path = ""
		}
	}

	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	logger, err := newLogger(cfg.Log)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)

	if path == "" {
		logger.Warn("no configuration file found; using built-in defaults and MOLISYNC_* environment variables")
	}
	return serve(cfg, logger)
}

func serve(cfg *config.Config, logger *slog.Logger) error {
	trustedPrefixes, err := config.ParseTrustedCIDRs(cfg.Proxy.TrustedCIDRs)
	if err != nil {
		return err
	}

	records, err := store.Open(store.Options{
		DataDir:      cfg.Storage.DataDir,
		BlobDir:      cfg.Storage.BlobDir,
		MaxItemBytes: cfg.Storage.MaxItemBytes,
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := records.Close(); err != nil {
			logger.Error("close record store", "error", err)
		}
	}()

	authMiddleware, err := auth.NewMiddleware(auth.Options{
		VerifyHeader:              cfg.Proxy.VerifyHeader,
		FingerprintHeader:         cfg.Proxy.FingerprintHeader,
		DNHeader:                  cfg.Proxy.DNHeader,
		RequiredVerifyValue:       cfg.Proxy.RequiredVerifyValue,
		DeviceIDSource:            cfg.Auth.DeviceIDSource,
		MaxHeaderValueBytes:       cfg.Auth.MaxHeaderValueBytes,
		MaxCommonNameLength:       cfg.Auth.MaxCommonNameLength,
		MaxDistinguishedNameBytes: cfg.Auth.MaxDistinguishedNameBytes,
	}, trustedPrefixes, logger)
	if err != nil {
		return err
	}

	devices := registry.New(cfg.Registry.MaxDevices, cfg.Registry.MaxEndpointsPerDevice, nil)
	hub, err := ws.NewHub(ws.Options{
		HandshakeTimeout:      cfg.WebSocket.HandshakeTimeout.Std(),
		HelloTimeout:          cfg.WebSocket.HelloTimeout.Std(),
		PingInterval:          cfg.WebSocket.PingInterval.Std(),
		PongTimeout:           cfg.WebSocket.PongTimeout.Std(),
		WriteTimeout:          cfg.WebSocket.WriteTimeout.Std(),
		ReadLimitBytes:        cfg.WebSocket.ReadLimitBytes,
		SendBuffer:            cfg.WebSocket.SendBuffer,
		HeartbeatInterval:     cfg.WebSocket.HeartbeatInterval.Std(),
		MaxMessagesPerSecond:  cfg.WebSocket.MaxMessagesPerSecond,
		AllowedOrigins:        cfg.WebSocket.AllowedOrigins,
		MaxEndpointsPerDevice: cfg.Registry.MaxEndpointsPerDevice,
		OfflineAfter:          cfg.Registry.OfflineAfter.Std(),
		SweepInterval:         cfg.Registry.SweepInterval.Std(),
	}, devices, logger)
	if err != nil {
		return err
	}
	hub.SetStore(records)

	sessions, err := p2p.NewStore(2 * time.Minute)
	if err != nil {
		return err
	}
	hub.SetSessions(sessions)
	hub.SetRelayPath("/api/v1/relay")
	hub.SetServerVersion(version)

	reporter, err := telemetry.New(records, telemetry.Options{
		Endpoint:        cfg.Telemetry.Endpoint,
		Token:           cfg.Telemetry.Token,
		Interval:        cfg.Telemetry.Interval.Std(),
		Timeout:         cfg.Telemetry.Timeout.Std(),
		ServerVersion:   version,
		ProtocolVersion: ws.ProtocolVersion,
		OnlineDevices:   func() int { return len(hub.Devices()) },
		Logger:          logger,
	})
	if err != nil {
		return err
	}

	var updates *updatecheck.Checker
	if cfg.UpdateCheck.Enabled {
		checker, checkErr := updatecheck.New(updatecheck.Options{
			Repository: cfg.UpdateCheck.Repository,
			Component:  updatecheck.ComponentServer,
			Current:    version,
			APIBase:    cfg.UpdateCheck.APIBase,
			Interval:   cfg.UpdateCheck.Interval.Std(),
			Timeout:    cfg.UpdateCheck.Timeout.Std(),
			Logger:     logger,
		})
		if checkErr != nil {
			// A development build reports "dev", which is not a semver value.
			// Update checking is optional, so this must not stop the server.
			logger.Info("server update check disabled", "reason", checkErr.Error())
		} else {
			updates = checker
		}
	}

	router := api.NewRouter(api.Deps{
		Auth:      authMiddleware,
		Hub:       hub,
		Store:     records,
		Sessions:  sessions,
		Relay:     p2p.NewRelay(15*time.Second, logger),
		Telemetry: reporter,
		Updates:   updates,
		Version:   version,
		Logger:    logger,
	})

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go reporter.Run(signalContext)
	if reporter.Enabled() {
		logger.Info("telemetry reporting enabled", "interval", cfg.Telemetry.Interval.Std())
	} else {
		logger.Info("telemetry reporting disabled", "reason", "no endpoint configured or disabled")
	}
	if updates != nil {
		go updates.Run(signalContext)
	}

	listener, err := net.Listen("tcp", cfg.Server.Listen)
	if err != nil {
		shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout.Std())
		defer cancel()
		_ = hub.Shutdown(shutdownContext)
		return fmt.Errorf("listen on %q: %w", cfg.Server.Listen, err)
	}

	server := &http.Server{
		Handler:           router,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout.Std(),
		ReadTimeout:       cfg.Server.ReadTimeout.Std(),
		WriteTimeout:      cfg.Server.WriteTimeout.Std(),
		IdleTimeout:       cfg.Server.IdleTimeout.Std(),
		MaxHeaderBytes:    cfg.Server.MaxHeaderBytes,
		BaseContext:       func(net.Listener) context.Context { return signalContext },
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	logger.Info("MoliSync server listening",
		"version", version,
		"address", cfg.Server.Listen,
		"trusted_proxies", len(trustedPrefixes),
		"device_id_source", cfg.Auth.DeviceIDSource,
	)

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()

	var serveErr error
	select {
	case <-signalContext.Done():
		logger.Info("shutdown signal received")
	case serveErr = <-serveErrors:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("http server failed", "error", serveErr)
		}
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout.Std())
	defer cancel()

	var shutdownErr error
	if err := hub.Shutdown(shutdownContext); err != nil && !errors.Is(err, context.Canceled) {
		shutdownErr = fmt.Errorf("shutdown websocket hub: %w", err)
	}
	if err := server.Shutdown(shutdownContext); err != nil {
		if shutdownErr == nil {
			shutdownErr = fmt.Errorf("shutdown http server: %w", err)
		}
		_ = server.Close()
	}
	logger.Info("server stopped")
	return shutdownErr
}

func newLogger(cfg config.LogConfig) (*slog.Logger, error) {
	var level slog.Level
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, fmt.Errorf("unsupported log level %q", cfg.Level)
	}

	options := &slog.HandlerOptions{Level: level}
	switch strings.ToLower(cfg.Format) {
	case "json":
		return slog.New(slog.NewJSONHandler(os.Stdout, options)), nil
	case "text":
		return slog.New(slog.NewTextHandler(os.Stdout, options)), nil
	default:
		return nil, fmt.Errorf("unsupported log format %q", cfg.Format)
	}
}
