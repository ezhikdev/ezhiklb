package domain

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
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
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Enabled          bool       `json:"enabled"`
	ListenAddress    string     `json:"listen_address"`
	ListenPort       uint16     `json:"listen_port"`
	Protocols        []Protocol `json:"protocols"`
	Scheduler        string     `json:"scheduler"`
	AffinitySecs     int        `json:"affinity_seconds"`
	RateLimitEnabled bool       `json:"rate_limit_enabled,omitempty"`
	RateLimitMbps    int        `json:"rate_limit_mbps,omitempty"`
	// SNI is the TLS server name a hidden profile routes by. Regular profiles
	// keep the value so toggling the profile mode does not lose it.
	SNI string `json:"sni,omitempty"`
	// MatchHost switches a hidden record from TLS SNI to plain-HTTP routing:
	// SNI is then compared with the Host header (empty matches any host) and
	// Path with the request path. CDNs that reach the node over http:// send
	// no SNI but keep the Host header.
	MatchHost bool      `json:"match_host,omitempty"`
	Path      string    `json:"path,omitempty"`
	Backends  []Backend `json:"backends"`
}

type ProfileConfig struct {
	SchemaVersion int  `json:"schema_version"`
	// Hidden profiles accept TCP on HiddenPorts only, route every connection
	// by TLS SNI and close the node's other unneeded inbound ports.
	Hidden      bool        `json:"hidden,omitempty"`
	HealthCheck HealthCheck `json:"health_check"`
	Listeners   []Listener  `json:"listeners"`
}

// HiddenPorts are the TCP ports a hidden profile listens on. Every record is
// reachable on both of them; the TLS SNI selects the record.
var HiddenPorts = []uint16{80, 443}

// HiddenMinAgentVersion is the first agent that understands hidden profiles.
const HiddenMinAgentVersion = "2.0.0"

// HTTPRouteMinAgentVersion is the first agent that routes hidden records by
// HTTP Host and path.
const HTTPRouteMinAgentVersion = "2.0.3"

var sniLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// NormalizeSNI returns the canonical lower-case form used for routing.
func NormalizeSNI(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}

// ValidSNI accepts a DNS host name or a single leading wildcard label
// ("*.example.com"). IP literals are rejected: TLS clients never send them.
func ValidSNI(value string) bool {
	name := NormalizeSNI(value)
	if name == "" || len(name) > 253 {
		return false
	}
	name = strings.TrimPrefix(name, "*.")
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !sniLabelPattern.MatchString(label) {
			return false
		}
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return false
	}
	// A purely numeric top-level label is never a valid public host name.
	tld := labels[len(labels)-1]
	return strings.Trim(tld, "0123456789") != ""
}

// pathPattern keeps request paths free of characters HAProxy's config parser
// treats specially (spaces, quotes, '#', '\', '$').
var pathPattern = regexp.MustCompile(`^/[A-Za-z0-9._~%:@!+,;=/-]*$`)

// NormalizePath trims spaces and a trailing slash ("/ws/" routes like "/ws");
// a lone "/" is kept and matches every path.
func NormalizePath(value string) string {
	path := strings.TrimSpace(value)
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
		if path == "" {
			path = "/"
		}
	}
	return path
}

// ValidPath accepts an absolute request path without query or fragment.
func ValidPath(value string) bool {
	path := NormalizePath(value)
	return len(path) <= 256 && pathPattern.MatchString(path) && !strings.Contains(path, "//")
}

// RouteLabel identifies a hidden record in statistics and service keys. SNI
// records keep the bare host name used since 2.0.0; HTTP records become
// "http://host/path" with "*" for any host.
func RouteLabel(listener Listener) string {
	if !listener.MatchHost {
		return NormalizeSNI(listener.SNI)
	}
	host := NormalizeSNI(listener.SNI)
	if host == "" {
		host = "*"
	}
	return "http://" + host + NormalizePath(listener.Path)
}

type Profile struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	CurrentRevision int64     `json:"current_revision"`
	AutoVersion     bool      `json:"auto_version"`
	Version         string    `json:"version"`
	SortOrder       int       `json:"sort_order"`
	// Hidden and Records summarise the current revision for profile lists.
	Hidden    bool      `json:"hidden,omitempty"`
	Records   int       `json:"records"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Revision struct {
	ID        int64         `json:"id"`
	ProfileID string        `json:"profile_id"`
	Number    int64         `json:"number"`
	Version   string        `json:"version"`
	Config    ProfileConfig `json:"config"`
	CreatedAt time.Time     `json:"created_at"`
}

type Node struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	IngressAddress  string           `json:"ingress_address"`
	ObservedAddress string           `json:"observed_address"`
	ProfileID       string           `json:"profile_id"`
	DesiredRevision int64            `json:"desired_revision"`
	AppliedRevision int64            `json:"applied_revision"`
	// AppliedProfileID is reported by 2.0+ agents. Revision numbers are per
	// profile, so the profile is needed to tell whether a node is in sync.
	AppliedProfileID string          `json:"applied_profile_id,omitempty"`
	AgentVersion    string           `json:"agent_version"`
	Status          string           `json:"status"`
	ApplyState      string           `json:"apply_state"`
	LastSeenAt      *time.Time       `json:"last_seen_at,omitempty"`
	OnlineSince     *time.Time       `json:"online_since,omitempty"`
	LastError       string           `json:"last_error,omitempty"`
	Metrics         *NodeMetrics     `json:"metrics,omitempty"`
	Diagnostics     *NodeDiagnostics `json:"diagnostics,omitempty"`
	UpdateTarget    string           `json:"update_target,omitempty"`
	UpdateState     string           `json:"update_state,omitempty"`
	UpdateError     string           `json:"update_error,omitempty"`
	TrafficAccountingEnabled bool     `json:"traffic_accounting_enabled"`
	TrafficRecordsEnabled    bool     `json:"traffic_records_enabled"`
	TrafficAutoResetEnabled  bool     `json:"traffic_auto_reset_enabled"`
	TrafficResetDay          int      `json:"traffic_reset_day"`
	TrafficIncomingBytes     uint64   `json:"traffic_incoming_bytes"`
	TrafficOutgoingBytes     uint64   `json:"traffic_outgoing_bytes"`
	TrafficResetAt           *time.Time `json:"traffic_reset_at,omitempty"`
	RestartNonce             int64    `json:"restart_nonce,omitempty"`
	// TCP memory tuning settings the agent receives with the desired state.
	TCPTuningEnabled bool `json:"tcp_tuning_enabled"`
	TCPMemoryPercent int  `json:"tcp_memory_percent"`
	SortOrder       int              `json:"sort_order"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

type NodeDiagnostics struct {
	IPVSAvailable           bool      `json:"ipvs_available"`
	HAProxyAvailable        bool      `json:"haproxy_available"`
	FirewallReady           bool      `json:"firewall_ready"`
	ServiceCount            int       `json:"service_count"`
	DestinationCount        int       `json:"destination_count"`
	Error                   string    `json:"error,omitempty"`
	TrafficControlAvailable bool      `json:"traffic_control_available"`
	RateLimitActive         bool      `json:"rate_limit_active"`
	RateLimitEntries        int       `json:"rate_limit_entries"`
	RateLimitDrops          uint64    `json:"rate_limit_drops"`
	RateLimitError          string    `json:"rate_limit_error,omitempty"`
	// HiddenMode reports that the node applies a hidden profile; the inbound
	// firewall then has to be in place for the node to be considered healthy.
	HiddenMode         bool      `json:"hidden_mode,omitempty"`
	InputFirewallReady bool      `json:"input_firewall_ready,omitempty"`
	AllowedInputPorts  []uint16  `json:"allowed_input_ports,omitempty"`
	// TCPMemory is reported by 2.0.4+ agents.
	TCPMemory *TCPMemoryStatus `json:"tcp_memory,omitempty"`
	CheckedAt time.Time        `json:"checked_at"`
}

// Kernel TCP buffer memory (net.ipv4.tcp_mem). The kernel default is sized
// for an ordinary server; a proxy holding two sockets per client runs into it
// and then prunes queued packets, which looks like heavy network loss.
const (
	TCPMemoryDefaultPercent = 70
	TCPMemoryMinPercent     = 10
	TCPMemoryMaxPercent     = 90
	// TCPTuningMinAgentVersion is the first agent that manages tcp_mem.
	TCPTuningMinAgentVersion = "2.0.4"
)

// TCPTuning is the per-node setting sent to the agent.
type TCPTuning struct {
	Enabled       bool `json:"enabled"`
	MemoryPercent int  `json:"memory_percent"`
}

// DefaultTCPTuning is what an agent applies before the panel answers.
func DefaultTCPTuning() TCPTuning {
	return TCPTuning{Enabled: true, MemoryPercent: TCPMemoryDefaultPercent}
}

// TCPMemoryStatus is the agent's view of TCP buffer memory. Byte values are
// converted from kernel pages; LimitBytes are tcp_mem's low, pressure and
// hard limits.
type TCPMemoryStatus struct {
	Automatic       bool             `json:"automatic"`
	Percent         int              `json:"percent"`
	AdaptivePercent int              `json:"adaptive_percent"`
	RAMBytes        uint64           `json:"ram_bytes"`
	AvailableBytes  uint64           `json:"available_bytes"`
	CPUs            int              `json:"cpus"`
	LimitBytes      [3]uint64        `json:"limit_bytes"`
	UsedBytes       uint64           `json:"used_bytes"`
	CeilingBytes    uint64           `json:"ceiling_bytes"`
	PressureEvents  uint64           `json:"pressure_events"`
	Warning         string           `json:"warning,omitempty"`
	Events          []TCPMemoryEvent `json:"events,omitempty"`
	CheckedAt       time.Time        `json:"checked_at"`
}

type TCPMemoryEvent struct {
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

type NodeMetrics struct {
	RAMUsedPercent float64   `json:"ram_used_percent"`
	CPUUsedPercent float64   `json:"cpu_used_percent"`
	Load1          float64   `json:"load_1"`
	CPUCores       int       `json:"cpu_cores"`
	RAMTotalBytes  uint64    `json:"ram_total_bytes,omitempty"`
	NetworkRxBPS   uint64    `json:"network_rx_bps"`
	NetworkTxBPS   uint64    `json:"network_tx_bps"`
	ActiveIPs      int       `json:"active_ips"`
	CollectedAt    time.Time `json:"collected_at"`
}

type NodeMetricPoint struct {
	NodeID         string    `json:"node_id"`
	RAMUsedPercent float64   `json:"ram_used_percent"`
	CPUUsedPercent float64   `json:"cpu_used_percent"`
	Load1          float64   `json:"load_1"`
	NetworkRxBPS   uint64    `json:"network_rx_bps"`
	NetworkTxBPS   uint64    `json:"network_tx_bps"`
	ActiveIPs      int       `json:"active_ips"`
	CollectedAt    time.Time `json:"collected_at"`
}

type SystemSettings struct {
	PanelPort       int `json:"panel_port"`
	AgentPort       int `json:"agent_port"`
	LegacyPanelPort int `json:"legacy_panel_port,omitempty"`
	LegacyAgentPort int `json:"legacy_agent_port,omitempty"`
}

type AuditEvent struct {
	ID         int64     `json:"id"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	Details    string    `json:"details"`
	CreatedAt  time.Time `json:"created_at"`
}

type NodeDesiredState struct {
	NodeID           string        `json:"node_id"`
	IngressAddress   string        `json:"ingress_address"`
	Revision         int64         `json:"revision"`
	ProfileID        string        `json:"profile_id"`
	ProfileName      string        `json:"profile_name"`
	HealthProbe      int64         `json:"health_probe"`
	ResetConnections bool          `json:"reset_connections,omitempty"`
	Decommission     bool          `json:"decommission"`
	UpdateVersion    string        `json:"update_version,omitempty"`
	RestartNonce     int64         `json:"restart_nonce,omitempty"`
	TCPTuning        *TCPTuning    `json:"tcp_tuning,omitempty"`
	Config           ProfileConfig `json:"config"`
}

type BackendHealth struct {
	NodeID          string    `json:"node_id,omitempty"`
	Address         string    `json:"address"`
	State           string    `json:"state"`
	ConsecutiveUp   int       `json:"consecutive_successes"`
	ConsecutiveDown int       `json:"consecutive_failures"`
	LatencyMillis   int64     `json:"latency_millis"`
	CheckedAt       time.Time `json:"checked_at"`
}

type ServiceStat struct {
	NodeID          string    `json:"node_id,omitempty"`
	Protocol        Protocol  `json:"protocol"`
	ListenAddress   string    `json:"listen_address"`
	ListenPort      uint16    `json:"listen_port"`
	SNI             string    `json:"sni,omitempty"`
	BackendAddress  string    `json:"backend_address,omitempty"`
	BackendPort     uint16    `json:"backend_port,omitempty"`
	Connections     uint64    `json:"connections"`
	IncomingPackets uint64    `json:"incoming_packets"`
	OutgoingPackets uint64    `json:"outgoing_packets"`
	IncomingBytes   uint64    `json:"incoming_bytes"`
	OutgoingBytes   uint64    `json:"outgoing_bytes"`
	CollectedAt     time.Time `json:"collected_at"`
}

// TrafficTotal is a persistent accounting counter. Empty protocol/address
// fields represent the node-wide counter; populated fields represent one
// profile record (listener protocol and endpoint).
type TrafficTotal struct {
	NodeID         string    `json:"node_id"`
	Protocol       Protocol  `json:"protocol,omitempty"`
	ListenAddress  string    `json:"listen_address,omitempty"`
	ListenPort     uint16    `json:"listen_port,omitempty"`
	SNI            string    `json:"sni,omitempty"`
	IncomingBytes  uint64    `json:"incoming_bytes"`
	OutgoingBytes  uint64    `json:"outgoing_bytes"`
	UpdatedAt      time.Time `json:"updated_at"`
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
	sniOwners := map[string]string{}
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
		if c.Hidden {
			problems = append(problems, validateHiddenListener(prefix, listener, sniOwners)...)
		} else {
			problems = append(problems, validateListenerSockets(prefix, listener, serviceKeys)...)
		}
		if listener.Scheduler != "wrr" && listener.Scheduler != "rr" {
			problems = append(problems, prefix+".scheduler must be wrr or rr")
		}
		if listener.AffinitySecs < 0 || listener.AffinitySecs > 86400 {
			problems = append(problems, prefix+".affinity_seconds must be between 0 and 86400")
		}
		if listener.RateLimitEnabled && (listener.RateLimitMbps < 1 || listener.RateLimitMbps > 100000) {
			problems = append(problems, prefix+".rate_limit_mbps must be between 1 and 100000 when enabled")
		}
		if !listener.RateLimitEnabled && listener.RateLimitMbps < 0 {
			problems = append(problems, prefix+".rate_limit_mbps cannot be negative")
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

// validateListenerSockets checks the explicit listen socket of a regular
// profile record and detects address:port:protocol conflicts between records.
func validateListenerSockets(prefix string, listener Listener, serviceKeys map[string]string) []string {
	var problems []string
	if listener.ListenPort == 0 {
		problems = append(problems, prefix+".listen_port is required")
	}
	if listener.ListenAddress != "" && listener.ListenAddress != "0.0.0.0" {
		if addr, err := netip.ParseAddr(listener.ListenAddress); err != nil || !addr.Is4() {
			problems = append(problems, prefix+".listen_address must be an IPv4 address")
		}
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
	return problems
}

// validateHiddenListener checks a hidden-profile record. Its socket is fixed
// (TCP on HiddenPorts), so only the route has to be valid and unique: an SNI
// record needs a host name; an HTTP record needs a Host, a path or both.
func validateHiddenListener(prefix string, listener Listener, owners map[string]string) []string {
	var problems []string
	valid := true
	host, path := strings.TrimSpace(listener.SNI), strings.TrimSpace(listener.Path)
	if listener.MatchHost {
		if host == "" && path == "" {
			problems = append(problems, prefix+" needs a host, a path or both")
			valid = false
		}
		if host != "" && !ValidSNI(host) {
			problems = append(problems, prefix+".sni must be a host name such as vpn.example.com or *.example.com")
			valid = false
		}
		if path != "" && !ValidPath(path) {
			problems = append(problems, prefix+".path must start with / and contain only letters, digits and . _ ~ % : @ ! + , ; = - /")
			valid = false
		}
	} else {
		if !ValidSNI(host) {
			problems = append(problems, prefix+".sni must be a host name such as vpn.example.com or *.example.com")
			valid = false
		}
		if path != "" {
			// TLS hides the path; only plain HTTP exposes it.
			problems = append(problems, prefix+".path requires Host matching")
			valid = false
		}
	}
	if valid {
		label := RouteLabel(listener)
		if owner, exists := owners[label]; exists {
			problems = append(problems, fmt.Sprintf("%s route %s is already used by listener %s", prefix, label, owner))
		}
		owners[label] = listener.ID
	}
	if len(listener.Protocols) != 1 || listener.Protocols[0] != ProtocolTCP {
		problems = append(problems, prefix+".protocols must be tcp in a hidden profile")
	}
	if listener.RateLimitEnabled {
		problems = append(problems, prefix+".rate_limit is not available in a hidden profile")
	}
	return problems
}

// HasHTTPRoutes reports whether a hidden profile routes any record by HTTP
// Host or path, which needs agent HTTPRouteMinAgentVersion.
func (c ProfileConfig) HasHTTPRoutes() bool {
	if !c.Hidden {
		return false
	}
	for _, listener := range c.Listeners {
		if listener.MatchHost {
			return true
		}
	}
	return false
}

func (c ProfileConfig) HasRateLimits() bool {
	for _, listener := range c.Listeners {
		if listener.Enabled && listener.RateLimitEnabled && listener.RateLimitMbps > 0 {
			return true
		}
	}
	return false
}

func DefaultProfileConfig() ProfileConfig {
	return ProfileConfig{
		SchemaVersion: SchemaVersion,
		HealthCheck:   DefaultHealthCheck(),
		Listeners:     []Listener{},
	}
}
