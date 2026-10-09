package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

type socketRunner struct {
	fakeRunner
	sockets string
}

func (r *socketRunner) Run(ctx context.Context, name string, args []string, input string) (string, error) {
	if name == "ss" {
		return r.sockets, nil
	}
	return r.fakeRunner.Run(ctx, name, args, input)
}

func TestPortConflictsRespectBindAddress(t *testing.T) {
	for _, test := range []struct {
		name     string
		address  string
		conflict bool
	}{
		{"same address", "198.51.100.20", true},
		{"other address", "198.51.100.10", false},
		{"IPv4 wildcard", "0.0.0.0", true},
		{"wildcard", "*", true},
		{"dual stack wildcard", "[::]", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &socketRunner{sockets: "tcp LISTEN 0 128 " + test.address + ":443 0.0.0.0:* users:((other))"}
			reconciler := NewReconciler(runner, filepath.Join(t.TempDir(), "state.json"), nil)
			err := reconciler.validatePortAvailability(context.Background(), []Service{{Protocol: domain.ProtocolTCP, Address: "198.51.100.20", Port: 443}})
			if (err != nil) != test.conflict {
				t.Fatalf("conflict = %v, want %v", err, test.conflict)
			}
		})
	}
}

func TestAppliedIngressSurvivesHealthCheckRefresh(t *testing.T) {
	reconciler := NewReconciler(&fakeRunner{}, filepath.Join(t.TempDir(), "state.json"), nil)
	if err := reconciler.saveState(AppliedState{Revision: 8, IngressAddress: "198.51.100.20"}); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.SaveHealthCheck(domain.DefaultHealthCheck()); err != nil {
		t.Fatal(err)
	}
	if got := reconciler.RestoredIngressAddress(); got != "198.51.100.20" {
		t.Fatalf("restored ingress = %q", got)
	}
}

type uninstallRunner struct {
	fakeRunner
	servicePresent bool
	failDelete     bool
	chainPresent   bool
}

func (r *uninstallRunner) Run(ctx context.Context, name string, args []string, input string) (string, error) {
	if name == "ipvsadm" && len(args) > 0 {
		if args[0] == "-D" {
			if r.failDelete || !r.servicePresent {
				return "", errors.New("delete failed")
			}
			r.servicePresent = false
		}
		if args[0] == "-Sn" && r.servicePresent {
			return "-A -u 198.51.100.20:443 -s rr", nil
		}
	}
	if name == "iptables" && r.chainPresent {
		if args[len(args)-1] == "-S" {
			return "-N EZHIKLB-FORWARD", nil
		}
		if strings.Contains(strings.Join(args, " "), "-F EZHIKLB-FORWARD") {
			return "", errors.New("flush failed")
		}
	}
	return r.fakeRunner.Run(ctx, name, args, input)
}

func TestUninstallPreservesOwnershipOnFailureAndCanRetry(t *testing.T) {
	runner := &uninstallRunner{servicePresent: true, failDelete: true}
	r := NewReconciler(runner, filepath.Join(t.TempDir(), "state.json"), nil)
	if err := r.saveState(AppliedState{Revision: 8, Services: []Service{{Protocol: domain.ProtocolUDP, Address: "198.51.100.20", Port: 443}}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Decommission(context.Background()); err == nil {
		t.Fatal("IPVS deletion failure was ignored")
	}
	state, err := r.loadState()
	if err != nil || len(state.Services) != 1 {
		t.Fatalf("ownership lost on failure: %#v, %v", state, err)
	}
	runner.failDelete = false
	runner.chainPresent = true
	if err := r.Decommission(context.Background()); err == nil {
		t.Fatal("firewall cleanup failure was ignored")
	}
	// IPVS is gone but state still owns it. The retry must accept verified
	// absence rather than becoming stuck on an already deleted service.
	runner.chainPresent = false
	if err := r.Decommission(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err = r.loadState()
	if err != nil || len(state.Services) != 0 {
		t.Fatalf("cleanup did not commit: %#v, %v", state, err)
	}
}

func TestRemovingLastTCPRouteStopsHAProxyWithoutEmptyConfigValidation(t *testing.T) {
	runner := &fakeRunner{}
	r := NewReconciler(runner, filepath.Join(t.TempDir(), "state.json"), nil)
	if err := os.WriteFile(r.haProxyConfigPath(), []byte("old config"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.applyHAProxy(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.haProxyConfigPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old HAProxy config retained: %v", err)
	}
	if len(runner.calls) != 1 || runner.calls[0].name != "systemctl" || strings.Join(runner.calls[0].args, " ") != "stop ezhiklb-haproxy.service" {
		t.Fatalf("unexpected cleanup commands: %#v", runner.calls)
	}
}
