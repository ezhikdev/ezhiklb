package agent

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

var tcDroppedPattern = regexp.MustCompile(`(?i)dropped\s+([0-9]+)`)

// CollectDiagnostics reports read-only data-plane checks.
func CollectDiagnostics(ctx context.Context, runner Runner, services []Service, controls ...[]TrafficControl) domain.NodeDiagnostics {
	result := domain.NodeDiagnostics{CheckedAt: time.Now().UTC(), ServiceCount: len(services)}
	for _, service := range services {
		result.DestinationCount += len(service.Destinations)
	}
	if _, err := runner.Run(ctx, "ipvsadm", []string{"-Ln"}, ""); err == nil {
		result.IPVSAvailable = true
	} else {
		result.Error = err.Error()
	}
	filter, filterErr := runner.Run(ctx, "iptables", []string{"-w", "5", "-S", "EZHIKLB-FORWARD"}, "")
	nat, natErr := runner.Run(ctx, "iptables", []string{"-w", "5", "-t", "nat", "-S", "EZHIKLB-SNAT"}, "")
	result.FirewallReady = filterErr == nil && natErr == nil && strings.Contains(filter, "EZHIKLB-FORWARD") && strings.Contains(nat, "EZHIKLB-SNAT")
	if result.Error == "" && (filterErr != nil || natErr != nil) {
		result.Error = "EzhikLB firewall chains are unavailable"
	}
	if len(controls) > 0 && len(controls[0]) > 0 {
		active := controls[0]
		if _, err := runner.Run(ctx, "tc", []string{"-V"}, ""); err == nil {
			result.TrafficControlAvailable = true
		} else {
			result.RateLimitError = err.Error()
		}
		listeners := map[string]bool{}
		for _, control := range active {
			listeners[control.ListenerID] = true
		}
		result.RateLimitActive = len(active) > 0
		result.RateLimitEntries = len(listeners)
		for _, control := range active {
			// Query only EzhikLB's own preference. Summing every egress filter on
			// the device would incorrectly attribute another application's drops.
			output, err := runner.Run(ctx, "tc", []string{"-s", "filter", "show", "dev", control.Device, "egress", "pref", strconv.Itoa(control.Preference)}, "")
			if err != nil {
				result.RateLimitError = err.Error()
				continue
			}
			for _, match := range tcDroppedPattern.FindAllStringSubmatch(output, -1) {
				if value, parseErr := strconv.ParseUint(match[1], 10, 64); parseErr == nil {
					result.RateLimitDrops += value
				}
			}
		}
	}
	return result
}
