package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ezhik-lb/ezhiklb/internal/agent"
	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

const version = "0.1.0-alpha.7.1"

type client struct {
	baseURL string
	token   string
	http    *http.Client
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	nodeID := env("EZHIKLB_NODE_ID", "local")
	panelURL := strings.TrimRight(env("EZHIKLB_PANEL_URL", "http://127.0.0.1:8080"), "/")
	if strings.HasPrefix(panelURL, "http://") && panelURL != "http://127.0.0.1:8080" && panelURL != "http://localhost:8080" && env("EZHIKLB_ALLOW_INSECURE", "0") != "1" {
		logger.Error("remote HTTP requires EZHIKLB_ALLOW_INSECURE=1; use HTTPS when the network is not trusted", "panel_url", panelURL)
		os.Exit(1)
	}
	token := os.Getenv("EZHIKLB_AGENT_TOKEN")
	if len(token) < 24 {
		logger.Error("EZHIKLB_AGENT_TOKEN must contain at least 24 characters")
		os.Exit(1)
	}
	api := &client{baseURL: panelURL, token: token, http: &http.Client{Timeout: 15 * time.Second}}
	runner := agent.ExecRunner{}
	reconciler := agent.NewReconciler(runner, env("EZHIKLB_AGENT_STATE", "/var/lib/ezhiklb-agent/state.json"), logger)
	monitor := agent.NewHealthMonitor(runner, reconciler, logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	desiredPoll := time.NewTicker(5 * time.Second)
	heartbeatPoll := time.NewTicker(15 * time.Second)
	defer desiredPoll.Stop()
	defer heartbeatPoll.Stop()
	var applied int64
	var lastHealthProbe int64
	var applyError string
	var healthCancel context.CancelFunc
	var healthMu sync.Mutex

	report := func() {
		stats, err := agent.CollectIPVSStats(ctx, runner)
		if err != nil {
			logger.Warn("collect IPVS stats", "error", err)
		}
		if err := api.heartbeat(ctx, nodeID, applied, applyError, monitor.Results(), stats); err != nil {
			logger.Error("send heartbeat", "error", err)
		}
	}
	reconcile := func() bool {
		desired, changed, err := api.desired(ctx, nodeID, applied, lastHealthProbe)
		if err != nil {
			logger.Error("fetch desired state", "error", err)
			return false
		}
		if !changed {
			return false
		}
		probeRequested := desired.HealthProbe != lastHealthProbe
		lastHealthProbe = desired.HealthProbe
		if desired.Revision != applied {
			logger.Info("applying desired revision", "revision", desired.Revision, "profile", desired.ProfileName)
			if err := reconciler.Reconcile(ctx, desired); err != nil {
				applyError = err.Error()
				logger.Error("apply desired state", "revision", desired.Revision, "error", err)
				return true
			} else {
				applied = desired.Revision
				applyError = ""
				healthMu.Lock()
				if healthCancel != nil {
					healthCancel()
				}
				healthCtx, stopHealth := context.WithCancel(ctx)
				healthCancel = stopHealth
				healthMu.Unlock()
				monitor.Reset()
				go monitor.Run(healthCtx, desired.Config.HealthCheck, reconciler.Services)
				return true
			}
		}
		if probeRequested && desired.Revision == applied {
			monitor.CheckNow(ctx, desired.Config.HealthCheck, reconciler.Services())
			logger.Info("manual health probe completed", "probe", desired.HealthProbe)
			return true
		}
		return false
	}

	reconcile()
	report()
	for {
		select {
		case <-ctx.Done():
			healthMu.Lock()
			if healthCancel != nil {
				healthCancel()
			}
			healthMu.Unlock()
			return
		case <-desiredPoll.C:
			if reconcile() {
				report()
			}
		case <-heartbeatPoll.C:
			report()
		}
	}
}

func (c *client) desired(ctx context.Context, nodeID string, knownRevision, knownHealthProbe int64) (domain.NodeDesiredState, bool, error) {
	var result domain.NodeDesiredState
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/agent/v1/nodes/"+nodeID+"/desired", nil)
	if err != nil {
		return result, false, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("If-None-Match", fmt.Sprintf(`"rev-%d-probe-%d"`, knownRevision, knownHealthProbe))
	response, err := c.http.Do(request)
	if err != nil {
		return result, false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		return result, false, nil
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		return result, false, fmt.Errorf("panel returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result); err != nil {
		return result, false, err
	}
	return result, true, nil
}

func (c *client) heartbeat(ctx context.Context, nodeID string, applied int64, applyError string, health []domain.BackendHealth, stats []domain.ServiceStat) error {
	body, _ := json.Marshal(map[string]any{"version": version, "applied_revision": applied, "apply_error": applyError, "health": health, "stats": stats})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/agent/v1/nodes/"+nodeID+"/heartbeat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		return errors.New(strings.TrimSpace(string(data)))
	}
	return nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
