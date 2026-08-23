package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ezhik-lb/ezhiklb/internal/api"
	"github.com/ezhik-lb/ezhiklb/internal/domain"
	"github.com/ezhik-lb/ezhiklb/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	dbPath := env("EZHIKLB_DATABASE", "/var/lib/ezhiklb/ezhiklb.db")
	st, err := store.Open(dbPath)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer st.Close()
	initial := domain.DefaultProfileConfig()
	profileName := "Default"
	if legacyPath := os.Getenv("EZHIKLB_LEGACY_CONFIG"); legacyPath != "" {
		if parsed, parseErr := domain.ParseLegacyFile(legacyPath); parseErr != nil && !errors.Is(parseErr, os.ErrNotExist) {
			logger.Error("parse legacy configuration", "error", parseErr)
			os.Exit(1)
		} else if parseErr == nil {
			initial = parsed
			profileName = "Migrated Ezhik UDP"
			logger.Info("legacy Ezhik UDP configuration detected", "path", legacyPath)
		}
	}
	if err := st.Bootstrap(context.Background(), os.Getenv("EZHIKLB_INGRESS_ADDRESS"), initial, profileName); err != nil {
		logger.Error("bootstrap database", "error", err)
		os.Exit(1)
	}
	port, err := strconv.Atoi(env("EZHIKLB_PORT", "8080"))
	if err != nil || port < 1 || port > 65535 {
		logger.Error("invalid EZHIKLB_PORT")
		os.Exit(1)
	}
	app, err := api.New(st, api.Options{
		AdminToken: os.Getenv("EZHIKLB_ADMIN_TOKEN"),
		AgentToken: os.Getenv("EZHIKLB_AGENT_TOKEN"),
		Secure:     env("EZHIKLB_SECURE_COOKIE", "1") == "1",
		WebDir:     env("EZHIKLB_WEB_DIR", "/usr/share/ezhiklb/web"),
		Logger:     logger,
	})
	if err != nil {
		logger.Error("configure server", "error", err)
		os.Exit(1)
	}
	httpServer := &http.Server{
		Addr:              api.ListenAddress(env("EZHIKLB_HOST", "127.0.0.1"), port),
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		logger.Info("EzhikLB panel started", "address", httpServer.Addr, "version", api.Version)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server", "error", err)
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
