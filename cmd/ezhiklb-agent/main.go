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
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ezhik-lb/ezhiklb/internal/agent"
	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

const version = "0.1.0-beta.3.4"

type client struct {
	baseURL string
	token   string
	http    *http.Client
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	nodeID := env("EZHIKLB_NODE_ID", "local")
	panelURL := strings.TrimRight(env("EZHIKLB_PANEL_URL", "http://127.0.0.1:8081"), "/")
	if strings.HasPrefix(panelURL, "http://") && !isLoopbackURL(panelURL) && env("EZHIKLB_ALLOW_INSECURE", "0") != "1" {
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
	metricsCollector := agent.NewMetricsCollector()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	desiredPoll := time.NewTicker(5 * time.Second)
	heartbeatPoll := time.NewTicker(15 * time.Second)
	defer desiredPoll.Stop()
	defer heartbeatPoll.Stop()
	applied, restoreErr := reconciler.Restore(ctx)
	if restoreErr != nil { logger.Error("restore last applied state", "error", restoreErr) } else if applied > 0 { logger.Info("restored last applied state", "revision", applied) }
	var lastHealthProbe int64
	var applyError string
	applyState := "connecting"
	if applied > 0 && restoreErr == nil { applyState = "applied" }
	var healthCancel context.CancelFunc
	var healthMu sync.Mutex
	var metrics domain.NodeMetrics
	var diagnostics domain.NodeDiagnostics
	var diagnosticsAt time.Time
	var updateState = "idle"
	var updateError string
	var lastUpdateTarget string
	var decommissioned bool
	restoreNeedsProfile := applied > 0 && restoreErr == nil
	if applied > 0 && restoreErr == nil {
		restoredHealth := reconciler.RestoredHealthCheck()
		healthCtx, stopHealth := context.WithCancel(ctx)
		healthCancel = stopHealth
		monitor.Reset()
		go monitor.Run(healthCtx, restoredHealth, reconciler.Services)
	}

	report := func() bool {
		stats, err := agent.CollectIPVSStats(ctx, runner)
		if err != nil {
			logger.Warn("collect IPVS stats", "error", err)
		}
		if collected, collectErr := metricsCollector.Collect(); collectErr != nil { logger.Warn("collect system metrics", "error", collectErr) } else { metrics = collected }
		if time.Since(diagnosticsAt) >= time.Minute { diagnostics = agent.CollectDiagnostics(ctx, runner, reconciler.Services()); diagnosticsAt = time.Now() }
		if err := api.heartbeat(ctx, nodeID, applied, applyError, applyState, monitor.Results(), stats, metrics, diagnostics, updateState, updateError, decommissioned); err != nil {
			logger.Error("send heartbeat", "error", err)
			return false
		}
		return true
	}
	reconcile := func() bool {
		knownRevision := applied
		refreshingRestored := restoreNeedsProfile
		if refreshingRestored { knownRevision = 0 }
		desired, changed, err := api.desired(ctx, nodeID, knownRevision, lastHealthProbe, lastUpdateTarget)
		if err != nil {
			logger.Error("fetch desired state", "error", err)
			return false
		}
		if !changed {
			return false
		}
		restoreNeedsProfile = false
		lastUpdateTarget = desired.UpdateVersion
		if desired.UpdateVersion != "" && desired.UpdateVersion != version {
			if domain.CompareVersions(desired.UpdateVersion, version) <= 0 {
				updateState, updateError = "completed", ""
				logger.Info("ignoring stale update target", "target", desired.UpdateVersion, "current", version)
				// Do not return here: after a binary restart the persisted state may
				// predate health-check persistence. The remainder of reconciliation
				// must still refresh the monitor from the desired profile.
			} else {
				updateState, updateError = "requested", ""
				report()
				updateCtx, cancelUpdate := context.WithTimeout(ctx, 3*time.Minute)
				err := agent.InstallAgentUpdate(updateCtx, desired.UpdateVersion, func(stage string) { updateState = stage; report() })
				cancelUpdate()
				if err != nil { updateState, updateError = "error", err.Error(); logger.Error("update agent", "version", desired.UpdateVersion, "error", err); return true }
				updateState = "restarting"
				report()
				logger.Info("agent update installed", "version", desired.UpdateVersion)
				// --no-block is required when a service asks systemd to restart itself:
				// waiting for the job would wait for this very process to terminate.
				_, err = runner.Run(context.Background(), "systemctl", []string{"--no-block", "restart", "ezhiklb-agent.service"}, "")
				if err != nil { updateState, updateError = "error", err.Error(); return true }
				return false
			}
		}
		if desired.Decommission {
			applyState = "decommissioning"
			if err := reconciler.Decommission(ctx); err != nil { applyError = err.Error(); applyState = "error"; logger.Error("decommission node", "error", err); return true }
			applyError = ""
			decommissioned = true
			logger.Info("node decommission completed")
			return true
		}
		probeRequested := desired.HealthProbe != lastHealthProbe
		lastHealthProbe = desired.HealthProbe
		if desired.Revision != applied {
			logger.Info("applying desired revision", "revision", desired.Revision, "profile", desired.ProfileName)
			applyState = "applying"
			report()
			if err := reconciler.Reconcile(ctx, desired); err != nil {
				applyError = err.Error()
				applyState = "error"
				logger.Error("apply desired state", "revision", desired.Revision, "error", err)
				return true
			} else {
				applied = desired.Revision
				applyError = ""
				applyState = "applied"
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
		if desired.Revision == applied && (healthCancel == nil || refreshingRestored) {
			if err := reconciler.SaveHealthCheck(desired.Config.HealthCheck); err != nil {
				applyError = "persist health-check settings: " + err.Error()
				applyState = "error"
				logger.Error("persist health-check settings", "error", err)
				return true
			}
			healthMu.Lock()
			if healthCancel != nil { healthCancel() }
			healthCtx, stopHealth := context.WithCancel(ctx)
			healthCancel = stopHealth
			healthMu.Unlock()
			monitor.Reset()
			go monitor.Run(healthCtx, desired.Config.HealthCheck, reconciler.Services)
			applyError = ""
			applyState = "applied"
			return true
		}
		if probeRequested && desired.Revision == applied {
			monitor.CheckNow(ctx, desired.Config.HealthCheck, reconciler.Services())
			logger.Info("manual health probe completed", "probe", desired.HealthProbe)
			return true
		}
		return false
	}

	reconcile()
	reported := report()
	if decommissioned && reported {
		_, _ = runner.Run(context.Background(), "systemctl", []string{"disable", "ezhiklb-agent.service"}, "")
		return
	}
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
				reported := report()
				if decommissioned && reported {
					_, _ = runner.Run(context.Background(), "systemctl", []string{"disable", "ezhiklb-agent.service"}, "")
					return
				}
			}
		case <-heartbeatPoll.C:
			report()
		}
	}
}

func (c *client) desired(ctx context.Context, nodeID string, knownRevision, knownHealthProbe int64, knownUpdate string) (domain.NodeDesiredState, bool, error) {
	var result domain.NodeDesiredState
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/agent/v1/nodes/"+nodeID+"/desired", nil)
	if err != nil {
		return result, false, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("If-None-Match", fmt.Sprintf(`"rev-%d-probe-%d-update-%s"`, knownRevision, knownHealthProbe, knownUpdate))
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

func (c *client) heartbeat(ctx context.Context, nodeID string, applied int64, applyError, applyState string, health []domain.BackendHealth, stats []domain.ServiceStat, metrics domain.NodeMetrics, diagnostics domain.NodeDiagnostics, updateState, updateError string, decommissioned bool) error {
	body, _ := json.Marshal(map[string]any{"version": version, "applied_revision": applied, "apply_error": applyError, "apply_state": applyState, "health": health, "stats": stats, "metrics": metrics, "diagnostics": diagnostics, "update_state": updateState, "update_error": updateError, "decommissioned": decommissioned})
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

func isLoopbackURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil { return false }
	host := parsed.Hostname()
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}
