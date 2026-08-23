package agent

import (
	"context"
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

