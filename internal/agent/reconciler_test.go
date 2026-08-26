package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

type recordedCall struct {
	name  string
	args  []string
	input string
}

type fakeRunner struct{ calls []recordedCall }

func (f *fakeRunner) Run(_ context.Context, name string, args []string, input string) (string, error) {
	f.calls = append(f.calls, recordedCall{name: name, args: append([]string(nil), args...), input: input})
	if name == "ip" && strings.Join(args, " ") == "-4 route get 192.0.2.10" {
		return "192.0.2.10 via 198.51.100.1 dev eth0 src 198.51.100.10", nil
	}
	if name == "ip" && strings.Join(args, " ") == "-4 route get 1.1.1.1" {
		return "1.1.1.1 via 198.51.100.1 dev eth0 src 198.51.100.10", nil
	}
	if name == "ip" && strings.Join(args, " ") == "-o -4 addr show" {
		return "2: eth0 inet 198.51.100.10/24 scope global eth0", nil
	}
	return "", nil
}

func TestCompileServicesCreatesTCPAndUDP(t *testing.T) {
	config := domain.DefaultProfileConfig()
	config.Listeners = []domain.Listener{{
		ID: "listener", Name: "Dual", Enabled: true, ListenPort: 8002, Protocols: []domain.Protocol{domain.ProtocolTCP, domain.ProtocolUDP}, Scheduler: "wrr",
		Backends: []domain.Backend{{ID: "backend", Address: "192.0.2.10", Port: 9000, Weight: 2, Enabled: true}},
	}}
	services := compileServices(config, "198.51.100.10")
	if len(services) != 2 {
		t.Fatalf("services = %d, want 2", len(services))
	}
	for _, service := range services {
		if service.Destinations[0].Port != 9000 {
			t.Fatalf("backend port = %d, want 9000", service.Destinations[0].Port)
		}
	}
}

func TestApplyIPVSNeverClearsGlobalTable(t *testing.T) {
	runner := &fakeRunner{}
	r := NewReconciler(runner, t.TempDir()+"/state.json", nil)
	service := Service{Protocol: domain.ProtocolUDP, Address: "198.51.100.10", Port: 8002, Scheduler: "wrr", Destinations: []Destination{{ID: "backend", Address: "192.0.2.10", Port: 9000, Weight: 2}}}
	if err := r.applyIPVS(context.Background(), nil, []Service{service}); err != nil {
		t.Fatal(err)
	}
	for _, call := range runner.calls {
		if call.name == "ipvsadm" && len(call.args) == 1 && call.args[0] == "-C" {
			t.Fatal("reconciler attempted to clear the global IPVS table")
		}
	}
}

func TestRestoreRebuildsSavedDataPlane(t *testing.T) {
	runner := &fakeRunner{}
	r := NewReconciler(runner, filepath.Join(t.TempDir(), "state.json"), nil)
	service := Service{Protocol: domain.ProtocolUDP, Address: "198.51.100.10", Port: 8002, Scheduler: "wrr", AffinitySecs: 10800, Destinations: []Destination{{ID: "backend", Address: "192.0.2.10", Port: 9000, Weight: 2}}}
	if err := r.saveState(AppliedState{Revision: 7, Services: []Service{service}}); err != nil {
		t.Fatal(err)
	}
	revision, err := r.Restore(context.Background())
	if err != nil { t.Fatal(err) }
	if revision != 7 { t.Fatalf("revision = %d, want 7", revision) }
	var serviceRestored, destinationRestored bool
	for _, call := range runner.calls {
		joined := strings.Join(call.args, " ")
		if call.name == "ipvsadm" && strings.Contains(joined, "-E -u 198.51.100.10:8002") { serviceRestored = true }
		if call.name == "ipvsadm" && strings.Contains(joined, "-e -u 198.51.100.10:8002 -r 192.0.2.10:9000") { destinationRestored = true }
	}
	if !serviceRestored || !destinationRestored { t.Fatalf("saved IPVS state was not restored: %#v", runner.calls) }
}

func TestSaveHealthCheckPreservesAppliedDataPlane(t *testing.T) {
	r := NewReconciler(&fakeRunner{}, filepath.Join(t.TempDir(), "state.json"), nil)
	service := Service{Protocol: domain.ProtocolUDP, Address: "198.51.100.10", Port: 8002}
	if err := r.saveState(AppliedState{Revision: 8, Services: []Service{service}}); err != nil {
		t.Fatal(err)
	}
	health := domain.DefaultHealthCheck()
	health.IntervalSeconds = 30
	if err := r.SaveHealthCheck(health); err != nil {
		t.Fatal(err)
	}
	state, err := r.loadState()
	if err != nil { t.Fatal(err) }
	if state.Revision != 8 { t.Fatalf("revision = %d, want 8", state.Revision) }
	if len(state.Services) != 1 || state.Services[0].Port != 8002 { t.Fatalf("services changed: %#v", state.Services) }
	if state.HealthCheck.IntervalSeconds != 30 || !state.HealthCheck.Enabled { t.Fatalf("health-check was not persisted: %#v", state.HealthCheck) }
}
