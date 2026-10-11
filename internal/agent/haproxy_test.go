package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

func TestRenderHAProxyConfigContainsOnlyTCP(t *testing.T) {
	services := []Service{
		{Protocol: domain.ProtocolTCP, ListenerID: "germany", Address: "198.51.100.10", Port: 443, Scheduler: "wrr", AffinitySecs: 900, Destinations: []Destination{{ID:"de-1",Address:"192.0.2.10",Port:8443,Weight:2}}},
		{Protocol: domain.ProtocolUDP, ListenerID: "dns", Address: "198.51.100.10", Port: 53, Scheduler: "rr"},
	}
	config := renderHAProxyConfig(services)
	for _, expected := range []string{"option splice-auto", "option tcplog", "timeout client 1h", "timeout server 1h", "frontend fe_germany", "bind 198.51.100.10:443", "backend be_germany", "stick on src", "server srv_de-1 192.0.2.10:8443 weight 2"} {
		if !strings.Contains(config, expected) { t.Fatalf("config does not contain %q:\n%s", expected, config) }
	}
	if strings.Contains(config, ":53") || strings.Contains(config, "be_dns") { t.Fatalf("UDP leaked into HAProxy config:\n%s", config) }
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

func TestParseActiveHAProxyClients(t *testing.T) {
	input := "0x1: proto=tcpv4 src=192.0.2.1:1234 fe=fe_vpn\n0x2: src=192.0.2.1:5678\n0x3: src=[2001:db8::1]:443\n0x4: src=invalid\n"
	clients, err := parseActiveHAProxyClients(strings.NewReader(input))
	if err != nil { t.Fatal(err) }
	seen := map[string]bool{}
	for _, client := range clients { seen[client] = true }
	if len(seen) != 2 || !seen["192.0.2.1"] || !seen["2001:db8::1"] { t.Fatalf("clients: %#v", clients) }
	if _, err := parseActiveHAProxyClients(failingHAProxyReader{}); err == nil { t.Fatal("socket failure ignored") }
	if _, err := parseHAProxyStats(failingHAProxyReader{}, nil); err == nil { t.Fatal("CSV socket failure ignored") }
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
		if err := os.WriteFile(path, []byte(renderHAProxyConfig(services)), 0600); err != nil { t.Fatal(err) }
		if output, err := exec.Command(haproxy, "-c", "-f", path).CombinedOutput(); err != nil { t.Fatalf("affinity %d: %v\n%s", affinity, err, output) }
	}
}

func BenchmarkParseActiveHAProxyClients(b *testing.B) {
	var data strings.Builder
	for i := 0; i < 10000; i++ { fmt.Fprintf(&data, "0x%x: proto=tcpv4 src=192.0.2.%d:%d fe=fe_vpn be=be_vpn srv=srv_a ts=00 age=123 calls=2 rate=0 cpu=0\n", i, i%250+1, i+1024) }
	input := data.String()
	b.ReportAllocs()
	b.SetBytes(int64(len(input)))
	for b.Loop() { if _, err := parseActiveHAProxyClients(strings.NewReader(input)); err != nil { b.Fatal(err) } }
}
