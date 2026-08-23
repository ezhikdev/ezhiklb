package domain

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = 1

type Protocol string

const (
	ProtocolTCP Protocol = "tcp"
	ProtocolUDP Protocol = "udp"
)

type HealthCheck struct {
	Enabled           bool `json:"enabled"`
	IntervalSeconds   int  `json:"interval_seconds"`
	TimeoutMillis     int  `json:"timeout_millis"`
	FailureThreshold  int  `json:"failure_threshold"`
	RecoveryThreshold int  `json:"recovery_threshold"`
}

func DefaultHealthCheck() HealthCheck {
	return HealthCheck{
		Enabled:           true,
		IntervalSeconds:   10,
		TimeoutMillis:     1000,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
	}
}

type Backend struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	Port    uint16 `json:"port"`
	Weight  int    `json:"weight"`
	Enabled bool   `json:"enabled"`
}

type Listener struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Enabled       bool       `json:"enabled"`
	ListenAddress string     `json:"listen_address"`
	ListenPort    uint16     `json:"listen_port"`
	Protocols     []Protocol `json:"protocols"`
	Scheduler     string     `json:"scheduler"`
	AffinitySecs  int        `json:"affinity_seconds"`
	Backends      []Backend  `json:"backends"`
}

type ProfileConfig struct {
	SchemaVersion int         `json:"schema_version"`
	HealthCheck   HealthCheck `json:"health_check"`
	Listeners     []Listener  `json:"listeners"`
}

type Profile struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	CurrentRevision int64     `json:"current_revision"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Revision struct {
	ID        int64         `json:"id"`
	ProfileID string        `json:"profile_id"`
	Number    int64         `json:"number"`
	Config    ProfileConfig `json:"config"`
	CreatedAt time.Time     `json:"created_at"`
}

type Node struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	IngressAddress  string     `json:"ingress_address"`
	ProfileID       string     `json:"profile_id"`
	DesiredRevision int64      `json:"desired_revision"`
	AppliedRevision int64      `json:"applied_revision"`
	AgentVersion    string     `json:"agent_version"`
	Status          string     `json:"status"`
	LastSeenAt      *time.Time `json:"last_seen_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type NodeDesiredState struct {
	NodeID          string        `json:"node_id"`
	IngressAddress  string        `json:"ingress_address"`
	Revision        int64         `json:"revision"`
	ProfileID       string        `json:"profile_id"`
	ProfileName     string        `json:"profile_name"`
	HealthProbe     int64         `json:"health_probe"`
	Config          ProfileConfig `json:"config"`
}

type BackendHealth struct {
	NodeID           string    `json:"node_id,omitempty"`
	Address          string    `json:"address"`
	State            string    `json:"state"`
	ConsecutiveUp    int       `json:"consecutive_successes"`
	ConsecutiveDown  int       `json:"consecutive_failures"`
	LatencyMillis    int64     `json:"latency_millis"`
	CheckedAt        time.Time `json:"checked_at"`
}

type ServiceStat struct {
	NodeID         string    `json:"node_id,omitempty"`
	Protocol       Protocol  `json:"protocol"`
	ListenAddress  string    `json:"listen_address"`
	ListenPort     uint16    `json:"listen_port"`
	BackendAddress string    `json:"backend_address,omitempty"`
	BackendPort    uint16    `json:"backend_port,omitempty"`
	Connections    uint64    `json:"connections"`
	IncomingPackets uint64   `json:"incoming_packets"`
	OutgoingPackets uint64   `json:"outgoing_packets"`
	IncomingBytes  uint64    `json:"incoming_bytes"`
	OutgoingBytes  uint64    `json:"outgoing_bytes"`
	CollectedAt    time.Time `json:"collected_at"`
}

func (c ProfileConfig) Validate() error {
	var problems []string
	if c.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Sprintf("schema_version must be %d", SchemaVersion))
	}

	h := c.HealthCheck
	if h.IntervalSeconds < 1 || h.IntervalSeconds > 3600 {
		problems = append(problems, "health_check.interval_seconds must be between 1 and 3600")
	}
	if h.TimeoutMillis < 100 || h.TimeoutMillis > 30000 {
		problems = append(problems, "health_check.timeout_millis must be between 100 and 30000")
	}
	if h.FailureThreshold < 1 || h.FailureThreshold > 100 {
		problems = append(problems, "health_check.failure_threshold must be between 1 and 100")
	}
	if h.RecoveryThreshold < 1 || h.RecoveryThreshold > 100 {
		problems = append(problems, "health_check.recovery_threshold must be between 1 and 100")
	}

	listenerIDs := map[string]bool{}
	serviceKeys := map[string]string{}
	for i, listener := range c.Listeners {
		prefix := fmt.Sprintf("listeners[%d]", i)
		if strings.TrimSpace(listener.ID) == "" {
			problems = append(problems, prefix+".id is required")
		} else if listenerIDs[listener.ID] {
			problems = append(problems, prefix+".id is duplicated")
		}
		listenerIDs[listener.ID] = true

		if strings.TrimSpace(listener.Name) == "" {
			problems = append(problems, prefix+".name is required")
		}
		if listener.ListenPort == 0 {
			problems = append(problems, prefix+".listen_port is required")
		}
		if listener.ListenAddress != "" && listener.ListenAddress != "0.0.0.0" {
			if addr, err := netip.ParseAddr(listener.ListenAddress); err != nil || !addr.Is4() {
				problems = append(problems, prefix+".listen_address must be an IPv4 address")
			}
		}
		if listener.Scheduler != "wrr" && listener.Scheduler != "rr" {
			problems = append(problems, prefix+".scheduler must be wrr or rr")
		}
		if listener.AffinitySecs < 0 || listener.AffinitySecs > 86400 {
			problems = append(problems, prefix+".affinity_seconds must be between 0 and 86400")
		}

		protocols := map[Protocol]bool{}
		for _, protocol := range listener.Protocols {
			if protocol != ProtocolTCP && protocol != ProtocolUDP {
				problems = append(problems, prefix+".protocols contains an unsupported value")
				continue
			}
			if protocols[protocol] {
				problems = append(problems, prefix+".protocols contains a duplicate")
			}
			protocols[protocol] = true
			key := fmt.Sprintf("%s:%d/%s", listener.ListenAddress, listener.ListenPort, protocol)
			if owner, exists := serviceKeys[key]; exists {
				problems = append(problems, fmt.Sprintf("%s conflicts with listener %s", prefix, owner))
			}
			wildcardKey := fmt.Sprintf("0.0.0.0:%d/%s", listener.ListenPort, protocol)
			if listener.ListenAddress != "0.0.0.0" {
				if owner, exists := serviceKeys[wildcardKey]; exists {
					problems = append(problems, fmt.Sprintf("%s conflicts with wildcard listener %s", prefix, owner))
				}
			} else {
				suffix := fmt.Sprintf(":%d/%s", listener.ListenPort, protocol)
				for existingKey, owner := range serviceKeys {
					if existingKey != key && strings.HasSuffix(existingKey, suffix) {
						problems = append(problems, fmt.Sprintf("%s conflicts with listener %s", prefix, owner))
					}
				}
			}
			serviceKeys[key] = listener.ID
		}
		if len(protocols) == 0 {
			problems = append(problems, prefix+".protocols must contain tcp, udp, or both")
		}

		backendIDs := map[string]bool{}
		backendEndpoints := map[string]bool{}
		enabledBackends := 0
		for j, backend := range listener.Backends {
			backendPrefix := fmt.Sprintf("%s.backends[%d]", prefix, j)
			if strings.TrimSpace(backend.ID) == "" {
				problems = append(problems, backendPrefix+".id is required")
			} else if backendIDs[backend.ID] {
				problems = append(problems, backendPrefix+".id is duplicated")
			}
			backendIDs[backend.ID] = true
			addr, err := netip.ParseAddr(backend.Address)
			if err != nil || !addr.Is4() {
				problems = append(problems, backendPrefix+".address must be an IPv4 address")
			}
			if backend.Port == 0 {
				problems = append(problems, backendPrefix+".port is required")
			}
			if backend.Weight < 1 || backend.Weight > 65535 {
				problems = append(problems, backendPrefix+".weight must be between 1 and 65535")
			}
			endpoint := fmt.Sprintf("%s:%d", backend.Address, backend.Port)
			if backendEndpoints[endpoint] {
				problems = append(problems, backendPrefix+" duplicates another backend endpoint")
			}
			backendEndpoints[endpoint] = true
			if backend.Enabled {
				enabledBackends++
			}
		}
		if listener.Enabled && enabledBackends == 0 {
			problems = append(problems, prefix+" must have at least one enabled backend")
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func DefaultProfileConfig() ProfileConfig {
	return ProfileConfig{
		SchemaVersion: SchemaVersion,
		HealthCheck:   DefaultHealthCheck(),
		Listeners:     []Listener{},
	}
}
