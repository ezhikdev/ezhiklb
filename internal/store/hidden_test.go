package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

func hiddenProfileConfig() domain.ProfileConfig {
	config := domain.DefaultProfileConfig()
	config.Hidden = true
	for _, item := range []struct{ id, sni string }{{"a", "a.example.com"}, {"b", "b.example.com"}} {
		config.Listeners = append(config.Listeners, domain.Listener{ID: item.id, Name: item.id, Enabled: true, ListenPort: 443, Protocols: []domain.Protocol{domain.ProtocolTCP}, Scheduler: "wrr", SNI: item.sni,
			Backends: []domain.Backend{{ID: item.id + "-b", Address: "192.0.2.10", Port: 8443, Weight: 1, Enabled: true}}})
	}
	return config
}

// TestStatisticsTablesGainSNIKey upgrades a 1.x database whose statistics
// primary keys have no SNI and checks that every counter survives.
func TestStatisticsTablesGainSNIKey(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Bootstrap(ctx, "198.51.100.10", domain.DefaultProfileConfig(), "Default", true); err != nil {
		t.Fatal(err)
	}
	now := formatTime(time.Now().UTC())
	for _, statement := range []string{
		`DROP TABLE service_stats`, `DROP TABLE traffic_totals`,
		`CREATE TABLE service_stats (node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE, protocol TEXT NOT NULL, listen_address TEXT NOT NULL, listen_port INTEGER NOT NULL, backend_address TEXT NOT NULL DEFAULT '', backend_port INTEGER NOT NULL DEFAULT 0, connections INTEGER NOT NULL DEFAULT 0, incoming_packets INTEGER NOT NULL DEFAULT 0, outgoing_packets INTEGER NOT NULL DEFAULT 0, incoming_bytes INTEGER NOT NULL DEFAULT 0, outgoing_bytes INTEGER NOT NULL DEFAULT 0, collected_at TEXT NOT NULL, PRIMARY KEY(node_id, protocol, listen_address, listen_port, backend_address, backend_port))`,
		`CREATE TABLE traffic_totals (node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE, protocol TEXT NOT NULL, listen_address TEXT NOT NULL, listen_port INTEGER NOT NULL, incoming_bytes INTEGER NOT NULL DEFAULT 0, outgoing_bytes INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL, PRIMARY KEY(node_id, protocol, listen_address, listen_port))`,
		`INSERT INTO service_stats(node_id,protocol,listen_address,listen_port,incoming_bytes,outgoing_bytes,collected_at) VALUES('local','udp','198.51.100.10',8002,100,200,'` + now + `')`,
		`INSERT INTO traffic_totals(node_id,protocol,listen_address,listen_port,incoming_bytes,outgoing_bytes,updated_at) VALUES('local','udp','198.51.100.10',8002,1000,2000,'` + now + `')`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}
	defer s.Close()
	if err := s.UpdateTrafficSettings(ctx, "local", true, true, false, 1); err != nil {
		t.Fatal(err)
	}
	// The preserved snapshot keeps the next delta continuous: 160-100.
	stats := []domain.ServiceStat{
		{Protocol: domain.ProtocolUDP, ListenAddress: "198.51.100.10", ListenPort: 8002, IncomingBytes: 160, OutgoingBytes: 260, CollectedAt: time.Now().UTC()},
		{Protocol: domain.ProtocolTCP, ListenAddress: "198.51.100.10", ListenPort: 443, SNI: "a.example.com", IncomingBytes: 10, OutgoingBytes: 20, CollectedAt: time.Now().UTC()},
		{Protocol: domain.ProtocolTCP, ListenAddress: "198.51.100.10", ListenPort: 443, SNI: "b.example.com", IncomingBytes: 30, OutgoingBytes: 40, CollectedAt: time.Now().UTC()},
	}
	if err := s.Heartbeat(ctx, "local", "2.0.0", "", "applied", 1, "", "", nil, stats, domain.NodeMetrics{CollectedAt: time.Now().UTC()}, domain.NodeDiagnostics{}, "idle", "", 0, false); err != nil {
		t.Fatal(err)
	}
	totals, err := s.ListTrafficTotals(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]domain.TrafficTotal{}
	for _, total := range totals {
		got[string(total.Protocol)+"/"+total.SNI] = total
	}
	if len(totals) != 3 || got["udp/"].IncomingBytes != 1060 || got["tcp/a.example.com"].IncomingBytes != 10 || got["tcp/b.example.com"].OutgoingBytes != 40 {
		t.Fatalf("totals = %#v", totals)
	}
	listed, err := s.ListStats(ctx)
	if err != nil || len(listed) != 3 {
		t.Fatalf("stats = %#v, %v", listed, err)
	}
}

func TestHiddenProfileRequiresCapableAgents(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "hidden.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Bootstrap(ctx, "198.51.100.10", domain.DefaultProfileConfig(), "Default", true); err != nil {
		t.Fatal(err)
	}
	profiles, _ := s.ListProfiles(ctx)
	profile := profiles[0]
	heartbeat := func(version string) {
		if err := s.Heartbeat(ctx, "local", version, "", "applied", 1, "", "", nil, nil, domain.NodeMetrics{}, domain.NodeDiagnostics{}, "idle", "", 0, false); err != nil {
			t.Fatal(err)
		}
	}
	heartbeat("1.2.2")
	if _, _, err := s.PublishRevision(ctx, profile.ID, profile.Name, "", hiddenProfileConfig(), true, "", false); !errors.Is(err, ErrHiddenUnsupported) {
		t.Fatalf("publish to an outdated node: %v", err)
	}
	hidden, _, err := s.CreateProfile(ctx, "Hidden", "", hiddenProfileConfig(), true, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AssignProfile(ctx, "local", hidden.ID); !errors.Is(err, ErrHiddenUnsupported) {
		t.Fatalf("assign to an outdated node: %v", err)
	}
	heartbeat("2.0.0")
	if err := s.AssignProfile(ctx, "local", hidden.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishRevision(ctx, hidden.ID, hidden.Name, "", hiddenProfileConfig(), true, "", false); err != nil {
		t.Fatal(err)
	}
	desired, err := s.DesiredState(ctx, "local")
	if err != nil || !desired.Config.Hidden || desired.Config.Listeners[1].SNI != "b.example.com" {
		t.Fatalf("desired = %#v, %v", desired.Config, err)
	}

	// UDP records need a newer agent than the hidden profile itself.
	withUDP := hiddenProfileConfig()
	withUDP.Listeners = append(withUDP.Listeners, domain.Listener{ID: "u", Name: "u", Enabled: true, ListenPort: 443, Protocols: []domain.Protocol{domain.ProtocolUDP}, Scheduler: "wrr",
		Backends: []domain.Backend{{ID: "u-b", Address: "192.0.2.10", Port: 51820, Weight: 1, Enabled: true}}})
	if _, _, err := s.PublishRevision(ctx, hidden.ID, hidden.Name, "", withUDP, true, "", false); !errors.Is(err, ErrHiddenUDPUnsupported) {
		t.Fatalf("publish UDP records to a %s node: %v", "2.0.0", err)
	}
	heartbeat(domain.HiddenUDPMinAgentVersion)
	if _, _, err := s.PublishRevision(ctx, hidden.ID, hidden.Name, "", withUDP, true, "", false); err != nil {
		t.Fatal(err)
	}

	// So do TCP port records.
	withPort := hiddenProfileConfig()
	withPort.Listeners = append(withPort.Listeners, domain.Listener{ID: "p", Name: "p", Enabled: true, ListenPort: 443, Protocols: []domain.Protocol{domain.ProtocolTCP}, Scheduler: "wrr", MatchPort: true,
		Backends: []domain.Backend{{ID: "p-b", Address: "192.0.2.10", Port: 8443, Weight: 1, Enabled: true}}})
	if _, _, err := s.PublishRevision(ctx, hidden.ID, hidden.Name, "", withPort, true, "", false); !errors.Is(err, ErrHiddenTCPPortUnsupported) {
		t.Fatalf("publish a TCP port record to a %s node: %v", domain.HiddenUDPMinAgentVersion, err)
	}
	heartbeat(domain.HiddenTCPPortMinAgentVersion)
	if _, _, err := s.PublishRevision(ctx, hidden.ID, hidden.Name, "", withPort, true, "", false); err != nil {
		t.Fatal(err)
	}
}

func TestDuplicateNamesReturnReadableErrors(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "names.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Bootstrap(ctx, "198.51.100.10", domain.DefaultProfileConfig(), "Default", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateProfile(ctx, "Default", "", domain.DefaultProfileConfig(), true, ""); !errors.Is(err, ErrProfileNameTaken) {
		t.Fatalf("duplicate profile: %v", err)
	}
	profiles, _ := s.ListProfiles(ctx)
	if _, _, err := s.CreateNode(ctx, "edge", "198.51.100.20", profiles[0].ID); err != nil {
		t.Fatal(err)
	}
	second, _, err := s.CreateNode(ctx, "edge-2", "198.51.100.21", profiles[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateNode(ctx, "edge", "198.51.100.22", profiles[0].ID); !errors.Is(err, ErrNodeNameTaken) {
		t.Fatalf("duplicate node: %v", err)
	}
	if err := s.UpdateNode(ctx, second.ID, "edge", "198.51.100.21"); !errors.Is(err, ErrNodeNameTaken) {
		t.Fatalf("rename to a taken name: %v", err)
	}
}
