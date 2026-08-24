package main

import (
	"context"
	"errors"
	"fmt"
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
	panelPort := parsePort(logger, "EZHIKLB_PORT", "8080")
	agentPort := parsePort(logger, "EZHIKLB_AGENT_PORT", "8081")
	settings, err := st.GetSystemSettings(context.Background(), domain.SystemSettings{PanelPort: panelPort, AgentPort: agentPort})
	if err != nil {
		logger.Error("load system settings", "error", err)
		os.Exit(1)
	}
	if settings.PanelPort == settings.AgentPort {
		logger.Error("panel and agent ports must be different")
		os.Exit(1)
	}
	restart := make(chan struct{}, 1)
	app, err := api.New(st, api.Options{
		AdminToken: os.Getenv("EZHIKLB_ADMIN_TOKEN"),
		AgentToken: os.Getenv("EZHIKLB_AGENT_TOKEN"),
		Secure:     env("EZHIKLB_SECURE_COOKIE", "1") == "1",
		WebDir:     env("EZHIKLB_WEB_DIR", "/usr/share/ezhiklb/web"),
		Logger:     logger,
		Settings:   settings,
		Restart: func() { select { case restart <- struct{}{}: default: } },
	})
	if err != nil {
		logger.Error("configure server", "error", err)
		os.Exit(1)
	}
	newServer := func(address string, handler http.Handler) *http.Server { return &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second} }
	panelServer := newServer(api.ListenAddress(env("EZHIKLB_HOST", "127.0.0.1"), settings.PanelPort), app.PanelHandler())
	agentHost := env("EZHIKLB_AGENT_HOST", "0.0.0.0")
	agentServer := newServer(api.ListenAddress(agentHost, settings.AgentPort), app.AgentHandler())
	servers := []struct { name string; server *http.Server }{{"panel", panelServer}, {"agent API", agentServer}}
	if settings.LegacyAgentPort > 0 && settings.LegacyAgentPort != settings.AgentPort && settings.LegacyAgentPort != settings.PanelPort {
		servers = append(servers, struct { name string; server *http.Server }{"legacy agent API", newServer(api.ListenAddress(agentHost, settings.LegacyAgentPort), app.AgentHandler())})
	}
	if settings.LegacyPanelPort > 0 && settings.LegacyPanelPort != settings.AgentPort && settings.LegacyPanelPort != settings.PanelPort && settings.LegacyPanelPort != settings.LegacyAgentPort {
		servers = append(servers, struct { name string; server *http.Server }{"legacy panel agent API", newServer(api.ListenAddress(agentHost, settings.LegacyPanelPort), app.AgentHandler())})
	}
	serverErrors := make(chan error, len(servers))
	serve := func(name string, server *http.Server) {
		logger.Info("EzhikLB "+name+" started", "address", server.Addr, "version", api.Version)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) { serverErrors <- fmt.Errorf("%s server: %w", name, err) }
	}
	for _, item := range servers { go serve(item.name, item.server) }
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	restarting := false
	failed := false
	select {
	case <-stop:
	case <-restart:
		restarting = true
	case err := <-serverErrors:
		failed = true
		logger.Error("serve", "error", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, item := range servers { _ = item.server.Shutdown(ctx) }
	if restarting || failed {
		_ = st.Close()
		if restarting { os.Exit(75) }
		os.Exit(1)
	}
}

func parsePort(logger *slog.Logger, key, fallback string) int {
	port, err := strconv.Atoi(env(key, fallback))
	if err != nil || port < 1 || port > 65535 {
		logger.Error("invalid port", "variable", key)
		os.Exit(1)
	}
	return port
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
