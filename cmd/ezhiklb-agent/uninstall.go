package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ezhik-lb/ezhiklb/internal/agent"
	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

// The script stops the running agent before calling this cleanup command.
// If cleanup fails, keep files so it can be retried without losing ownership.
func uninstallNode(logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	nodeID := env("EZHIKLB_NODE_ID", "local")
	api := &client{
		baseURL: strings.TrimRight(env("EZHIKLB_PANEL_URL", "http://127.0.0.1:8081"), "/"),
		token: os.Getenv("EZHIKLB_AGENT_TOKEN"),
		http: &http.Client{Timeout: 15 * time.Second},
	}
	requested := false
	if nodeID != "local" {
		if err := api.requestDecommission(ctx, nodeID); err != nil {
			logger.Warn("panel unavailable or request refused; remove the node from the panel manually after local uninstall", "error", err)
		} else {
			requested = true
		}
	}
	reconciler := agent.NewReconciler(agent.ExecRunner{}, env("EZHIKLB_AGENT_STATE", "/var/lib/ezhiklb-agent/state.json"), logger)
	if err := reconciler.Decommission(ctx); err != nil {
		return err
	}
	if requested {
		if err := api.heartbeat(ctx, nodeID, 0, "", "decommissioning", nil, nil, domain.NodeMetrics{}, domain.NodeDiagnostics{}, "idle", "", 0, true); err != nil {
			logger.Warn("local cleanup completed; force-delete the pending node from the panel", "error", err)
		}
	}
	return nil
}

func (c *client) requestDecommission(ctx context.Context, nodeID string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/agent/v1/nodes/"+nodeID+"/decommission", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("panel decommission request returned %s", response.Status)
	}
	return nil
}
