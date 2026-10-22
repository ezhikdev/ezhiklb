package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

func TestRenderHAProxyConfigContainsOnlyTCP(t *testing.T) {
	services := []Service{
		{Protocol: domain.ProtocolTCP, ListenerID: "germany", Address: "198.51.100.10", Port: 443, Scheduler: "wrr", AffinitySecs: 900, Destinations: []Destination{{ID:"de-1",Address:"192.0.2.10",Port:8443,Weight:2}}},
		{Protocol: domain.ProtocolUDP, ListenerID: "dns", Address: "198.51.100.10", Port: 53, Scheduler: "rr"},
	}
	config := renderHAProxyConfig(services, "")
	for _, expected := range []string{"maxconn 200000", "option splice-auto", "timeout client 1h", "timeout server 1h", "frontend fe_germany", "bind 198.51.100.10:443", "backend be_germany", "stick on src", "server srv_de-1 192.0.2.10:8443 weight 2"} {
		if !strings.Contains(config, expected) { t.Fatalf("config does not contain %q:\n%s", expected, config) }
	}
	if strings.Contains(config, ":53") || strings.Contains(config, "be_dns") { t.Fatalf("UDP leaked into HAProxy config:\n%s", config) }
}

// Per-connection logs cost CPU and fill small disks on busy bridges.
func TestHAProxyConfigHasNoTrafficLog(t *testing.T) {
	services := []Service{{Protocol: domain.ProtocolTCP, ListenerID: "vpn", Address: "198.51.100.10", Port: 443, Scheduler: "rr", Destinations: []Destination{{ID: "a", Address: "192.0.2.10", Port: 8443, Weight: 1}}}}
	hidden := []Service{{Protocol: domain.ProtocolTCP, ListenerID: "h", Address: "198.51.100.10", Port: 443, SNI: "a.test", Scheduler: "rr", Destinations: []Destination{{ID: "a", Address: "192.0.2.10", Port: 8443, Weight: 1}}}}
	for _, config := range []string{renderHAProxyConfig(services, ""), renderHAProxyConfig(hidden, "/tmp/reject.pem")} {
		for _, line := range strings.Split(config, "\n") {
			line = strings.TrimSpace(line)
			if line == "log global" || strings.HasPrefix(line, "log ") || strings.Contains(line, "tcplog") { t.Fatalf("traffic log enabled by %q:\n%s", line, config) }
		}
	}
}

func TestHAProxyWeightNormalization(t *testing.T) {
	service := Service{Destinations: []Destination{{Weight: 1000}, {Weight: 500}, {Weight: 1}}}
	for _, test := range []struct{ weight, want int }{{1000, 256}, {500, 128}, {1, 1}, {0, 0}, {-1, 0}} {
		if got := haProxyWeight(service, test.weight); got != test.want { t.Fatalf("weight %d: got %d, want %d", test.weight, got, test.want) }
	}
}

func TestParseHAProxyStats(t *testing.T) {
	services := []Service{{Protocol: domain.ProtocolTCP, ListenerID: "vpn", Address: "198.51.100.10", Port: 443, Destinations: []Destination{{ID: "a", Address: "192.0.2.10", Port: 8443}}}}
	input := "# pxname,svname,stot,bin,bout,\nbe_vpn,BACKEND,12,100,200,\nbe_vpn,srv_a,10,80,160,\nfe_vpn,FRONTEND,12,100,200,\nbe_other,BACKEND,5,50,60,\nbe_vpn,srv_removed,1,20,40,\n"
	stats, err := parseHAProxyStats(strings.NewReader(input), services)
	if err != nil { t.Fatal(err) }
	if len(stats) != 2 { t.Fatalf("unexpected stats: %#v", stats) }
	if stats[0].Connections != 12 || stats[0].IncomingBytes != 100 || stats[0].OutgoingBytes != 200 || stats[0].BackendAddress != "" { t.Fatalf("backend total: %#v", stats[0]) }
	if stats[1].BackendAddress != "192.0.2.10" || stats[1].BackendPort != 8443 || stats[1].Connections != 10 || stats[1].IncomingBytes != 80 { t.Fatalf("destination: %#v", stats[1]) }
	for _, stat := range stats { if stat.Protocol != domain.ProtocolTCP || stat.ListenPort != 443 || stat.ListenAddress != "198.51.100.10" || stat.CollectedAt.IsZero() { t.Fatalf("metadata: %#v", stat) } }
	if _, err := parseHAProxyStats(strings.NewReader(input+"broken,row\n"), services); err == nil { t.Fatal("truncated CSV accepted") }
}

type failingHAProxyReader struct{}
func (failingHAProxyReader) Read([]byte) (int, error) { return 0, errors.New("socket read failed") }

func TestParseSocketPeers(t *testing.T) {
	input := "0 0 198.51.100.10:443 192.0.2.1:1234\n0 0 198.51.100.10:80 192.0.2.1:5678\n0 0 [::ffff:198.51.100.10]:443 [::ffff:192.0.2.7]:9\n0 0 [2001:db8::10]:443 [2001:db8::1]:443\nbroken\n"
	clients, err := parseSocketPeers(strings.NewReader(input))
	if err != nil { t.Fatal(err) }
	seen := map[string]bool{}
	for _, client := range clients { seen[client] = true }
	if len(seen) != 3 || !seen["192.0.2.1"] || !seen["192.0.2.7"] || !seen["2001:db8::1"] { t.Fatalf("clients: %#v", clients) }
	if _, err := parseSocketPeers(failingHAProxyReader{}); err == nil { t.Fatal("read failure ignored") }
}

func TestActiveTCPClientsArgs(t *testing.T) {
	services := []Service{
		{Protocol: domain.ProtocolTCP, Address: "198.51.100.10", Port: 8443},
		{Protocol: domain.ProtocolTCP, Address: "198.51.100.10", Port: 443, SNI: "a.test"},
		{Protocol: domain.ProtocolTCP, Address: "198.51.100.10", Port: 443, SNI: "b.test"},
		{Protocol: domain.ProtocolTCP, Address: "0.0.0.0", Port: 9000},
		{Protocol: domain.ProtocolUDP, Address: "198.51.100.10", Port: 53},
	}
	got := strings.Join(activeTCPClientsArgs(services), " ")
	want := "-H -t -n state established ( src 198.51.100.10:8443 or src 198.51.100.10:80 or src 198.51.100.10:443 or sport = :9000 )"
	if got != want { t.Fatalf("ss args:\n got %s\nwant %s", got, want) }
	if args := activeTCPClientsArgs([]Service{{Protocol: domain.ProtocolUDP, Port: 53}}); args != nil { t.Fatalf("UDP-only node runs ss: %v", args) }
}

// An unchanged config must not start another HAProxy worker generation;
// runtime weights are re-synced through the admin socket instead.
func TestUnchangedHAProxyConfigSkipsReload(t *testing.T) {
	dir := t.TempDir()
	listener, err := net.Listen("unix", filepath.Join(dir, "admin.sock"))
	if err != nil { t.Skipf("unix sockets unavailable: %v", err) }
	defer listener.Close()
	var commands []string
	var mu sync.Mutex
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil { return }
			line, _ := bufio.NewReader(conn).ReadString('\n')
			mu.Lock(); commands = append(commands, strings.TrimSpace(line)); mu.Unlock()
			conn.Close()
		}
	}()
	oldSocket := haProxySocket
	haProxySocket = filepath.Join(dir, "admin.sock")
	defer func() { haProxySocket = oldSocket }()

	runner := &fakeRunner{}
	r := NewReconciler(runner, filepath.Join(dir, "state.json"), nil)
	services := []Service{{Protocol: domain.ProtocolTCP, ListenerID: "vpn", Address: "198.51.100.10", Port: 443, Scheduler: "rr", Destinations: []Destination{{ID: "a", Address: "192.0.2.10", Port: 8443, Weight: 3}}}}
	reloads := func() int {
		count := 0
		for _, call := range runner.calls { if call.name == "systemctl" && strings.Join(call.args, " ") == "reload-or-restart ezhiklb-haproxy.service" { count++ } }
		return count
	}
	if err := r.applyHAProxy(context.Background(), services); err != nil { t.Fatal(err) }
	if err := r.applyHAProxy(context.Background(), services); err != nil { t.Fatal(err) }
	if got := reloads(); got != 1 { t.Fatalf("reloads = %d, want 1", got) }
	mu.Lock()
	if len(commands) != 1 || commands[0] != "set server be_vpn/srv_a weight 3" { t.Fatalf("socket commands: %#v", commands) }
	mu.Unlock()

	services[0].Destinations[0].Weight = 5
	if err := r.applyHAProxy(context.Background(), services); err != nil { t.Fatal(err) }
	if got := reloads(); got != 2 { t.Fatalf("changed config: reloads = %d, want 2", got) }

	// A failed weight sync falls back to a reload.
	listener.Close()
	if err := r.applyHAProxy(context.Background(), services); err != nil { t.Fatal(err) }
	if got := reloads(); got != 3 { t.Fatalf("sync failure: reloads = %d, want 3", got) }
}

func TestResetPublicationReloadsHAProxyOnce(t *testing.T) {
	runner := &fakeRunner{}
	r := NewReconciler(runner, filepath.Join(t.TempDir(), "state.json"), nil)
	config := domain.DefaultProfileConfig()
	config.Listeners = []domain.Listener{{
		ID: "vpn", Name: "VPN", Enabled: true, ListenAddress: "0.0.0.0", ListenPort: 443,
		Protocols: []domain.Protocol{domain.ProtocolTCP}, Scheduler: "wrr", AffinitySecs: 900,
		Backends: []domain.Backend{{ID: "a", Address: "192.0.2.10", Port: 8443, Weight: 2, Enabled: true}},
	}}
	services := compileServices(config, "198.51.100.10")
	if err := r.saveState(AppliedState{Revision: 1, Services: services, DataPlaneVersion: "haproxy-tcp"}); err != nil { t.Fatal(err) }
	if err := r.Reconcile(context.Background(), domain.NodeDesiredState{Revision: 2, Config: config, ResetConnections: true}); err != nil { t.Fatal(err) }
	reloads := 0
	for _, call := range runner.calls { if call.name == "systemctl" && strings.Join(call.args, " ") == "reload-or-restart ezhiklb-haproxy.service" { reloads++ } }
	if reloads != 1 { t.Fatalf("got %d HAProxy reloads, want 1", reloads) }
}

// The release job installs HAProxy; local workspaces without it skip this check.
func TestHAProxyAcceptsGeneratedConfig(t *testing.T) {
	haproxy, err := exec.LookPath("haproxy")
	if err != nil { t.Skip("HAProxy is checked on Linux in the release workflow") }
	for _, affinity := range []int{0, 900, 86400} {
		services := []Service{{Protocol: domain.ProtocolTCP, ListenerID: "vpn", Address: "127.0.0.1", Port: 14443, Scheduler: "wrr", AffinitySecs: affinity, Destinations: []Destination{{ID: "a", Address: "127.0.0.1", Port: 18443, Weight: 1000}, {ID: "b", Address: "127.0.0.1", Port: 18444, Weight: 1}}}}
		path := filepath.Join(t.TempDir(), "haproxy.cfg")
		if err := os.WriteFile(path, []byte(renderHAProxyConfig(services, "")), 0600); err != nil { t.Fatal(err) }
		if output, err := exec.Command(haproxy, "-c", "-f", path).CombinedOutput(); err != nil { t.Fatalf("affinity %d: %v\n%s", affinity, err, output) }
	}
}

func BenchmarkParseSocketPeers(b *testing.B) {
	var data strings.Builder
	for i := 0; i < 10000; i++ { fmt.Fprintf(&data, "0 0 198.51.100.10:443 192.0.2.%d:%d\n", i%250+1, i+1024) }
	input := data.String()
	b.ReportAllocs()
	b.SetBytes(int64(len(input)))
	for b.Loop() { if _, err := parseSocketPeers(strings.NewReader(input)); err != nil { b.Fatal(err) } }
}
