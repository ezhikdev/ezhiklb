package agent

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

const (
	ReachabilityUnknown = "unknown"
	ReachabilityUp      = "reachable"
	ReachabilityDown    = "unreachable"
)

type HealthMonitor struct {
	runner     Runner
	reconciler *Reconciler
	logger     *slog.Logger
	mu         sync.RWMutex
	results    map[string]domain.BackendHealth
}

func NewHealthMonitor(runner Runner, reconciler *Reconciler, logger *slog.Logger) *HealthMonitor {
	return &HealthMonitor{runner: runner, reconciler: reconciler, logger: logger, results: map[string]domain.BackendHealth{}}
}

func (m *HealthMonitor) Run(ctx context.Context, config domain.HealthCheck, services func() []Service) {
	if !config.Enabled {
		m.logger.Info("health monitor disabled")
		return
	}
	if config.IntervalSeconds < 1 || config.TimeoutMillis < 1 || config.FailureThreshold < 1 || config.RecoveryThreshold < 1 {
		m.logger.Error("health monitor has invalid settings",
			"interval_seconds", config.IntervalSeconds,
			"timeout_millis", config.TimeoutMillis,
			"failure_threshold", config.FailureThreshold,
			"recovery_threshold", config.RecoveryThreshold)
		return
	}
	interval := time.Duration(config.IntervalSeconds) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	m.logger.Info("health monitor started", "interval_seconds", config.IntervalSeconds, "failure_threshold", config.FailureThreshold, "recovery_threshold", config.RecoveryThreshold)
	defer m.logger.Info("health monitor stopped")
	m.checkAll(ctx, config, services())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkAll(ctx, config, services())
		}
	}
}

func (m *HealthMonitor) Results() []domain.BackendHealth {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]domain.BackendHealth, 0, len(m.results))
	for _, item := range m.results {
		result = append(result, item)
	}
	return result
}

func (m *HealthMonitor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results = map[string]domain.BackendHealth{}
}

func (m *HealthMonitor) CheckNow(ctx context.Context, config domain.HealthCheck, services []Service) {
	if !config.Enabled { return }
	m.checkAll(ctx, config, services)
}

func (m *HealthMonitor) checkAll(ctx context.Context, config domain.HealthCheck, services []Service) {
	addresses := map[string]bool{}
	for _, service := range services {
		for _, destination := range service.Destinations {
			addresses[destination.Address] = true
		}
	}
	for address := range addresses {
		started := time.Now()
		pingCtx, cancel := context.WithTimeout(ctx, time.Duration(config.TimeoutMillis)*time.Millisecond)
		_, err := m.runner.Run(pingCtx, "ping", []string{"-n", "-c", "1", "-W", strconv.Itoa(max(1, (config.TimeoutMillis+999)/1000)), address}, "")
		cancel()

		m.mu.Lock()
		result := m.results[address]
		result.Address = address
		result.CheckedAt = time.Now().UTC()
		previous := result.State
		if err == nil {
			result.ConsecutiveUp++
			result.ConsecutiveDown = 0
			result.LatencyMillis = time.Since(started).Milliseconds()
			if result.State != ReachabilityUp && result.ConsecutiveUp >= config.RecoveryThreshold {
				result.State = ReachabilityUp
			}
		} else {
			result.ConsecutiveDown++
			result.ConsecutiveUp = 0
			result.LatencyMillis = 0
			if result.ConsecutiveDown >= config.FailureThreshold {
				result.State = ReachabilityDown
			}
		}
		m.results[address] = result
		m.mu.Unlock()

		if result.State != previous && result.State != ReachabilityUnknown {
			m.applyAddressState(ctx, services, address, result.State)
			m.logger.Info("backend reachability changed", "address", address, "state", result.State)
		}
	}
}

func (m *HealthMonitor) applyAddressState(ctx context.Context, services []Service, address string, state string) {
	m.reconciler.mu.Lock()
	defer m.reconciler.mu.Unlock()
	for _, service := range services {
		for _, destination := range service.Destinations {
			if destination.Address != address {
				continue
			}
			weight := destination.Weight
			if state == ReachabilityDown {
				weight = 0
			}
			weightCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := m.reconciler.setDestinationWeight(weightCtx, service, destination, weight)
			cancel()
			if err != nil {
				m.logger.Error("update health weight", "service", serviceKey(service), "backend", destinationKey(destination), "error", err)
			}
		}
	}
}
