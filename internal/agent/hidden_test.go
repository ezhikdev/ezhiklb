package agent

import (
	"context"
	"crypto/tls"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

// scriptedRunner extends fakeRunner with canned outputs per command line.
type scriptedRunner struct {
	fakeRunner
	outputs map[string]string
}

func (s *scriptedRunner) Run(ctx context.Context, name string, args []string, input string) (string, error) {
	if output, ok := s.outputs[name+" "+strings.Join(args, " ")]; ok {
		s.calls = append(s.calls, recordedCall{name: name, args: append([]string(nil), args...), input: input})
		return output, nil
	}
	return s.fakeRunner.Run(ctx, name, args, input)
}

func (s *scriptedRunner) ran(name, args string) bool {
	return slices.ContainsFunc(s.calls, func(call recordedCall) bool { return call.name == name && strings.Join(call.args, " ") == args })
}

func (s *scriptedRunner) inputOf(name, marker string) string {
	for _, call := range s.calls {
		if call.name == name && strings.Contains(call.input, marker) {
			return call.input
		}
	}
	return ""
}

func hiddenConfig() domain.ProfileConfig {
	config := domain.DefaultProfileConfig()
	config.Hidden = true
	config.Listeners = []domain.Listener{
		{ID: "wild", Name: "Wildcard", Enabled: true, ListenAddress: "203.0.113.5", ListenPort: 443, Protocols: []domain.Protocol{domain.ProtocolTCP}, Scheduler: "wrr", AffinitySecs: 900, SNI: "*.Example.org",
			Backends: []domain.Backend{{ID: "w1", Address: "192.0.2.10", Port: 8443, Weight: 1, Enabled: true}}},
		{ID: "exact", Name: "Exact", Enabled: true, ListenPort: 443, Protocols: []domain.Protocol{domain.ProtocolTCP}, Scheduler: "rr", SNI: "vpn.example.org",
			Backends: []domain.Backend{{ID: "e1", Address: "192.0.2.10", Port: 9443, Weight: 2, Enabled: true}, {ID: "e2", Address: "192.0.2.11", Port: 9443, Weight: 1, Enabled: false}}},
		{ID: "off", Name: "Disabled", Enabled: false, ListenPort: 443, Protocols: []domain.Protocol{domain.ProtocolTCP}, Scheduler: "rr", SNI: "off.example.org",
			Backends: []domain.Backend{{ID: "o1", Address: "192.0.2.10", Port: 1443, Weight: 1, Enabled: true}}},
	}
	return config
}

func TestCompileHiddenServices(t *testing.T) {
	services := compileServices(hiddenConfig(), "198.51.100.10")
	if len(services) != 2 {
		t.Fatalf("services = %#v, want the two enabled records", services)
	}
	for _, service := range services {
		if service.Protocol != domain.ProtocolTCP || service.Address != "198.51.100.10" || service.Port != 443 || service.SNI == "" {
			t.Fatalf("hidden record must use the ingress address and the SNI frontend: %#v", service)
		}
	}
	if keys := []string{serviceKey(services[0]), serviceKey(services[1])}; keys[0] == keys[1] {
		t.Fatalf("records sharing the frontend collapsed into one key: %v", keys)
	}
	for _, service := range services {
		if service.ListenerID == "wild" && service.SNI != "*.example.org" {
			t.Fatalf("SNI not normalized: %q", service.SNI)
		}
		if service.ListenerID == "exact" && len(service.Destinations) != 1 {
			t.Fatalf("disabled backend compiled: %#v", service.Destinations)
		}
	}
}

func TestRenderHiddenHAProxyConfig(t *testing.T) {
	config := renderHAProxyConfig(compileServices(hiddenConfig(), "198.51.100.10"), "/var/lib/ezhiklb-agent/sni-reject.pem")
	for _, expected := range []string{
		"frontend fe_ezhiklb.sni", "bind 198.51.100.10:80\n", "bind 198.51.100.10:443\n",
		"tcp-request inspect-delay 5s", "tcp-request content accept if { req.ssl_sni -m found }",
		"use_backend be_exact if { req.ssl_sni -i vpn.example.org }",
		"use_backend be_wild if { req.ssl_sni -m end -i .example.org }",
		"use_backend be_ezhiklb.reject-tls if { req.ssl_hello_type 1 }", "default_backend be_ezhiklb.reject-plain",
		"ssl crt /var/lib/ezhiklb-agent/sni-reject.pem strict-sni", "http-request deny deny_status 400",
		"http-request redirect scheme https code 301 if { hdr(host),field(1,:) -i vpn.example.org }",
		"http-request redirect scheme https code 301 if { hdr(host),field(1,:) -m end -i .example.org }",
		"backend be_exact\n  balance roundrobin", "server srv_e1 192.0.2.10:9443 weight 2", "stick on src",
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("config does not contain %q:\n%s", expected, config)
		}
	}
	if strings.Index(config, "be_exact if") > strings.Index(config, "be_wild if") {
		t.Fatalf("exact names must be matched before wildcards:\n%s", config)
	}
	if strings.Contains(config, "frontend fe_exact") || strings.Contains(config, "off.example.org") {
		t.Fatalf("hidden record got its own frontend or a disabled record leaked:\n%s", config)
	}
}

func httpRoutesConfig() domain.ProfileConfig {
	config := hiddenConfig()
	route := func(id, host, path string) domain.Listener {
		return domain.Listener{ID: id, Name: id, Enabled: true, ListenPort: 443, Protocols: []domain.Protocol{domain.ProtocolTCP}, Scheduler: "wrr", SNI: host, MatchHost: true, Path: path,
			Backends: []domain.Backend{{ID: id + "-1", Address: "192.0.2.20", Port: 8080, Weight: 1, Enabled: true}}}
	}
	config.Listeners = append(config.Listeners, route("anyws", "", "/ws/"), route("hostws", "CDN.example.org", "/ws"), route("hostws2", "cdn.example.org", "/ws/deep"), route("cdn", "cdn.example.org", ""))
	return config
}

func TestRenderHiddenHTTPRoutes(t *testing.T) {
	services := compileServices(httpRoutesConfig(), "198.51.100.10")
	config := renderHAProxyConfig(services, "/tmp/reject.pem")
	for _, expected := range []string{
		"tcp-request content accept if HTTP\n",
		"use_backend be_hostws2 if HTTP { hdr(host),field(1,:) -i cdn.example.org } { path /ws/deep }",
		"use_backend be_hostws if HTTP { hdr(host),field(1,:) -i cdn.example.org } { path_beg /ws/ }",
		"use_backend be_cdn if HTTP { hdr(host),field(1,:) -i cdn.example.org }\n",
		"use_backend be_anyws if HTTP { path /ws }", "use_backend be_anyws if HTTP { path_beg /ws/ }",
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("config does not contain %q:\n%s", expected, config)
		}
	}
	order := []string{"be_exact if", "be_wild if", "be_hostws2 if", "be_hostws if", "be_cdn if", "be_anyws if", "be_ezhiklb.reject-tls if"}
	for i := 1; i < len(order); i++ {
		if strings.Index(config, order[i-1]) > strings.Index(config, order[i]) {
			t.Fatalf("%q must come before %q:\n%s", order[i-1], order[i], config)
		}
	}
	if strings.Contains(config, "redirect scheme https code 301 if { hdr(host),field(1,:) -i cdn.example.org }") {
		t.Fatalf("HTTP records must not redirect to https:\n%s", config)
	}
	labels := map[string]bool{}
	for _, service := range services {
		labels[service.SNI] = true
	}
	for _, label := range []string{"vpn.example.org", "http://*/ws", "http://cdn.example.org/ws", "http://cdn.example.org"} {
		if !labels[label] {
			t.Fatalf("missing route label %q in %#v", label, labels)
		}
	}
	if strings.Contains(renderHAProxyConfig(compileServices(hiddenConfig(), "198.51.100.10"), "/tmp/reject.pem"), "accept if HTTP") {
		t.Fatal("an SNI-only profile must keep the 2.0.0 frontend")
	}
}

func TestHAProxyAcceptsHiddenConfig(t *testing.T) {
	haproxy, err := exec.LookPath("haproxy")
	if err != nil {
		t.Skip("HAProxy is checked on Linux in the release workflow")
	}
	dir := t.TempDir()
	certificate := filepath.Join(dir, "sni-reject.pem")
	if err := ensureRejectCertificate(certificate); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "haproxy.cfg")
	if err := os.WriteFile(path, []byte(renderHAProxyConfig(compileServices(portRoutesConfig(), "127.0.0.1"), certificate)), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(haproxy, "-c", "-f", path).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

func TestRejectCertificateIsCreatedOnceAndValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sni-reject.pem")
	if err := ensureRejectCertificate(path); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if _, err := tls.X509KeyPair(first, first); err != nil {
		t.Fatalf("invalid PEM: %v", err)
	}
	if err := ensureRejectCertificate(path); err != nil {
		t.Fatal(err)
	}
	if second, _ := os.ReadFile(path); string(second) != string(first) {
		t.Fatal("a valid certificate was regenerated")
	}
}

func TestDetectInputPortsKeepsSSHAndPanel(t *testing.T) {
	runner := &scriptedRunner{outputs: map[string]string{"ss -H -lntup": strings.Join([]string{
		`tcp LISTEN 0 128 0.0.0.0:2222 0.0.0.0:* users:(("sshd",pid=10,fd=3))`,
		`tcp LISTEN 0 128 [::]:2222 [::]:* users:(("sshd",pid=10,fd=4))`,
		`tcp LISTEN 0 4096 0.0.0.0:8081 0.0.0.0:* users:(("ezhiklb",pid=11,fd=7))`,
		`tcp LISTEN 0 4096 127.0.0.1:8080 0.0.0.0:* users:(("ezhiklb",pid=11,fd=6))`,
		`tcp LISTEN 0 511 0.0.0.0:3000 0.0.0.0:* users:(("node",pid=12,fd=6))`,
		`tcp LISTEN 0 511 0.0.0.0:9000 0.0.0.0:* users:(("ezhiklb-agent",pid=13,fd=6))`,
		`udp UNCONN 0 0 0.0.0.0:5353 0.0.0.0:* users:(("sshd",pid=10,fd=9))`,
	}, "\n")}}
	r := NewReconciler(runner, filepath.Join(t.TempDir(), "state.json"), nil)
	ports, err := r.detectInputPorts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint16{22, 80, 443, 2222, 8081}; !slices.Equal(ports, want) {
		t.Fatalf("ports = %v, want %v", ports, want)
	}
}

func TestRenderInputRules(t *testing.T) {
	ports := make([]uint16, 0, 20)
	for port := uint16(1000); port < 1020; port++ {
		ports = append(ports, port)
	}
	rules := renderInputRules(ports, []uint16{443}, false)
	lines := strings.Split(strings.TrimSpace(rules), "\n")
	if lines[len(lines)-2] != "-A EZHIKLB-INPUT -j DROP" || lines[len(lines)-1] != "COMMIT" {
		t.Fatalf("DROP must be the final rule:\n%s", rules)
	}
	for _, expected := range []string{"-i lo -j RETURN", "--ctstate ESTABLISHED,RELATED -j RETURN", "-p icmp -j RETURN", "--sport 67 --dport 68", "--dports 1000,1001,1002,1003,1004,1005,1006,1007,1008,1009,1010,1011,1012,1013,1014 -j RETURN", "--dports 1015,1016,1017,1018,1019 -j RETURN", "-p udp -m multiport --dports 443 -j RETURN"} {
		if !strings.Contains(rules, expected) {
			t.Fatalf("rules do not contain %q:\n%s", expected, rules)
		}
	}
	if strings.Contains(rules, "ACCEPT") {
		t.Fatalf("the chain must only narrow the host policy:\n%s", rules)
	}
	if v6 := renderInputRules([]uint16{22}, nil, true); !strings.Contains(v6, "-p ipv6-icmp -j RETURN") || strings.Contains(v6, "-p icmp ") {
		t.Fatalf("IPv6 rules must keep neighbour discovery working:\n%s", v6)
	}
	if plain := renderInputRules([]uint16{22}, nil, false); strings.Contains(plain, "-p udp -m multiport") {
		t.Fatalf("UDP opened without a hidden UDP record:\n%s", plain)
	}
}

func hiddenUDPListener(id string, port uint16) domain.Listener {
	return domain.Listener{ID: id, Name: id, Enabled: true, ListenPort: port, Protocols: []domain.Protocol{domain.ProtocolUDP}, Scheduler: "wrr", AffinitySecs: 300,
		Backends: []domain.Backend{{ID: id + "-b", Address: "192.0.2.10", Port: 51820, Weight: 1, Enabled: true}}}
}

func TestCompileHiddenUDPServices(t *testing.T) {
	config := hiddenConfig()
	config.Listeners = append(config.Listeners, hiddenUDPListener("udp443", 443), hiddenUDPListener("udp80", 80))
	services := compileServices(config, "198.51.100.10")
	udp := onlyProtocol(services, domain.ProtocolUDP)
	if len(udp) != 2 || len(onlyProtocol(services, domain.ProtocolTCP)) != 2 {
		t.Fatalf("services = %#v, want two SNI records and two UDP records", services)
	}
	for _, service := range udp {
		if service.Address != "198.51.100.10" || service.SNI != "" || service.HTTPRoute || len(service.Destinations) != 1 || service.AffinitySecs != 300 {
			t.Fatalf("hidden UDP record must be a plain IPVS service on the ingress address: %#v", service)
		}
	}
	if ports := hiddenUDPPorts(services); !slices.Equal(ports, []uint16{80, 443}) {
		t.Fatalf("UDP ports = %v", ports)
	}
}

func TestHiddenReconcileOpensUDPRecordPorts(t *testing.T) {
	runner := &scriptedRunner{outputs: map[string]string{}}
	r := NewReconciler(runner, filepath.Join(t.TempDir(), "state.json"), nil)
	ctx := context.Background()
	config := hiddenConfig()
	config.Listeners = append(config.Listeners, hiddenUDPListener("udp443", 443))
	if err := r.Reconcile(ctx, domain.NodeDesiredState{Revision: 1, Config: config}); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(runner.calls, func(call recordedCall) bool {
		return call.name == "ipvsadm" && strings.Contains(strings.Join(call.args, " "), "-u 198.51.100.10:443")
	}) {
		t.Fatalf("IPVS UDP service not created: %#v", runner.calls)
	}
	// UDP records never reach HAProxy: the SNI frontend keeps TCP 80/443.
	if haproxy, err := os.ReadFile(r.haProxyConfigPath()); err != nil || strings.Contains(string(haproxy), "udp443") {
		t.Fatalf("UDP record rendered into HAProxy: %v\n%s", err, haproxy)
	}
	rules := runner.inputOf("iptables-restore", "EZHIKLB-INPUT")
	if !strings.Contains(rules, "-A EZHIKLB-INPUT -p udp -m multiport --dports 443 -j RETURN") || !strings.Contains(rules, "-p tcp -m multiport --dports 22,80,443 -j RETURN") {
		t.Fatalf("UDP record port not opened:\n%s", rules)
	}
	if v6 := runner.inputOf("ip6tables-restore", "EZHIKLB-INPUT"); strings.Contains(v6, "-p udp -m multiport") {
		t.Fatalf("IPv6 must stay closed for UDP:\n%s", v6)
	}

	// Removing the UDP record closes the port again.
	runner.calls = nil
	if err := r.Reconcile(ctx, domain.NodeDesiredState{Revision: 2, Config: hiddenConfig()}); err != nil {
		t.Fatal(err)
	}
	if rules := runner.inputOf("iptables-restore", "EZHIKLB-INPUT"); rules == "" || strings.Contains(rules, "-p udp -m multiport") {
		t.Fatalf("UDP port stayed open after the record was removed:\n%s", rules)
	}
	if !slices.ContainsFunc(runner.calls, func(call recordedCall) bool {
		return call.name == "ipvsadm" && strings.HasPrefix(strings.Join(call.args, " "), "-D -u 198.51.100.10:443")
	}) {
		t.Fatalf("IPVS UDP service not removed: %#v", runner.calls)
	}
}

func TestHiddenReconcileAppliesAndRemovesInputFirewall(t *testing.T) {
	runner := &scriptedRunner{outputs: map[string]string{}}
	statePath := filepath.Join(t.TempDir(), "state.json")
	r := NewReconciler(runner, statePath, nil)
	ctx := context.Background()
	if err := r.Reconcile(ctx, domain.NodeDesiredState{Revision: 1, Config: hiddenConfig()}); err != nil {
		t.Fatal(err)
	}
	if rules := runner.inputOf("iptables-restore", "EZHIKLB-INPUT"); !strings.Contains(rules, "--dports 22,80,443 -j RETURN") {
		t.Fatalf("inbound firewall not applied:\n%s", rules)
	}
	state, err := r.loadState()
	if err != nil || !state.Hidden || !slices.Equal(state.InputPorts, []uint16{22, 80, 443}) {
		t.Fatalf("state = %#v, %v", state, err)
	}
	if _, err := os.Stat(r.sniRejectCertificatePath()); err != nil {
		t.Fatalf("reject certificate missing: %v", err)
	}
	config, err := os.ReadFile(r.haProxyConfigPath())
	if err != nil || !strings.Contains(string(config), "frontend fe_ezhiklb.sni") {
		t.Fatalf("HAProxy config: %v\n%s", err, config)
	}

	runner.calls = nil
	regular := domain.DefaultProfileConfig()
	regular.Listeners = []domain.Listener{{ID: "tcp", Name: "TCP", Enabled: true, ListenPort: 8443, Protocols: []domain.Protocol{domain.ProtocolTCP}, Scheduler: "wrr",
		Backends: []domain.Backend{{ID: "b", Address: "192.0.2.10", Port: 8443, Weight: 1, Enabled: true}}}}
	if err := r.Reconcile(ctx, domain.NodeDesiredState{Revision: 2, Config: regular}); err != nil {
		t.Fatal(err)
	}
	if !runner.ran("iptables", "-w 5 -t filter -D INPUT -j EZHIKLB-INPUT") || !runner.ran("iptables", "-w 5 -t filter -X EZHIKLB-INPUT") {
		t.Fatalf("leaving hidden mode kept the inbound firewall: %#v", runner.calls)
	}
	if state, _ := r.loadState(); state.Hidden || len(state.InputPorts) != 0 {
		t.Fatalf("state still hidden: %#v", state)
	}

	runner.calls = nil
	if err := r.Reconcile(ctx, domain.NodeDesiredState{Revision: 3, Config: regular}); err != nil {
		t.Fatal(err)
	}
	for _, call := range runner.calls {
		if strings.Contains(strings.Join(call.args, " ")+call.input, "EZHIKLB-INPUT") {
			t.Fatalf("a regular node ran an inbound firewall command: %#v", call)
		}
	}
}

func TestHiddenReconcileRefusesOccupiedWebPorts(t *testing.T) {
	runner := &scriptedRunner{outputs: map[string]string{"ss -H -lntup": `tcp LISTEN 0 511 0.0.0.0:80 0.0.0.0:* users:(("nginx",pid=5,fd=6))`}}
	r := NewReconciler(runner, filepath.Join(t.TempDir(), "state.json"), nil)
	err := r.Reconcile(context.Background(), domain.NodeDesiredState{Revision: 1, Config: hiddenConfig()})
	if err == nil || !strings.Contains(err.Error(), "198.51.100.10:80 is occupied") {
		t.Fatalf("err = %v, want a port 80 conflict", err)
	}
	if runner.inputOf("iptables-restore", "EZHIKLB-INPUT") != "" {
		t.Fatal("inbound firewall applied although the profile could not be served")
	}
}

func TestEnsureInputFirewallTracksPortChanges(t *testing.T) {
	runner := &scriptedRunner{outputs: map[string]string{
		"iptables -w 5 -S EZHIKLB-INPUT": "-N EZHIKLB-INPUT\n-A EZHIKLB-INPUT -j DROP\n",
	}}
	r := NewReconciler(runner, filepath.Join(t.TempDir(), "state.json"), nil)
	ctx := context.Background()
	if diagnostics, err := r.EnsureInputFirewall(ctx); err != nil || diagnostics.HiddenMode {
		t.Fatalf("regular node reported hidden mode: %#v, %v", diagnostics, err)
	}
	if err := r.saveState(AppliedState{Hidden: true, InputPorts: []uint16{22, 80, 443}}); err != nil {
		t.Fatal(err)
	}
	runner.outputs["ss -H -lntup"] = `tcp LISTEN 0 128 0.0.0.0:2200 0.0.0.0:* users:(("sshd",pid=10,fd=3))`
	diagnostics, err := r.EnsureInputFirewall(ctx)
	if err != nil || !diagnostics.HiddenMode || !diagnostics.InputFirewallReady || !slices.Equal(diagnostics.AllowedInputPorts, []uint16{22, 80, 443, 2200}) {
		t.Fatalf("diagnostics = %#v, %v", diagnostics, err)
	}
	if state, _ := r.loadState(); !slices.Equal(state.InputPorts, []uint16{22, 80, 443, 2200}) {
		t.Fatalf("new SSH port not persisted: %v", state.InputPorts)
	}

	// A chain that lost its UDP rule (e.g. rewritten by another tool) is
	// re-applied even though the TCP ports did not change.
	udpService := Service{Protocol: domain.ProtocolUDP, Address: "198.51.100.10", Port: 443, Scheduler: "wrr", ListenerID: "udp"}
	if err := r.saveState(AppliedState{Hidden: true, InputPorts: []uint16{22, 80, 443, 2200}, Services: []Service{udpService}}); err != nil {
		t.Fatal(err)
	}
	runner.calls = nil
	diagnostics, err = r.EnsureInputFirewall(ctx)
	if !strings.Contains(runner.inputOf("iptables-restore", "EZHIKLB-INPUT"), "-p udp -m multiport --dports 443 -j RETURN") {
		t.Fatalf("missing UDP rule not repaired: %#v", runner.calls)
	}
	// The scripted chain listing still lacks the UDP rule: not ready.
	if !slices.Equal(diagnostics.AllowedInputUDPPorts, []uint16{443}) || diagnostics.InputFirewallReady || err == nil {
		t.Fatalf("diagnostics = %#v, %v", diagnostics, err)
	}
	runner.outputs["iptables -w 5 -S EZHIKLB-INPUT"] = "-N EZHIKLB-INPUT\n-A EZHIKLB-INPUT -p udp -m multiport --dports 443 -j RETURN\n-A EZHIKLB-INPUT -j DROP\n"
	if diagnostics, err := r.EnsureInputFirewall(ctx); err != nil || !diagnostics.InputFirewallReady {
		t.Fatalf("diagnostics = %#v, %v", diagnostics, err)
	}
}

func TestParseHAProxyStatsLabelsHiddenRecords(t *testing.T) {
	services := compileServices(hiddenConfig(), "198.51.100.10")
	input := "# pxname,svname,stot,bin,bout,\nbe_exact,BACKEND,3,30,60,\nbe_wild,BACKEND,4,40,80,\nbe_ezhiklb.reject-tls,BACKEND,9,9,9,\n"
	stats, err := parseHAProxyStats(strings.NewReader(input), services)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 || stats[0].SNI != "vpn.example.org" || stats[1].SNI != "*.example.org" {
		t.Fatalf("stats = %#v", stats)
	}
}

// portRoutesConfig adds a TCP port record on each hidden port to httpRoutesConfig.
func portRoutesConfig() domain.ProfileConfig {
	config := httpRoutesConfig()
	port := func(id string, number uint16) domain.Listener {
		return domain.Listener{ID: id, Name: id, Enabled: true, ListenPort: number, Protocols: []domain.Protocol{domain.ProtocolTCP}, Scheduler: "wrr", MatchPort: true, SNI: "ignored.example.org",
			Backends: []domain.Backend{{ID: id + "-1", Address: "192.0.2.30", Port: number, Weight: 1, Enabled: true}}}
	}
	config.Listeners = append(config.Listeners, port("any443", 443), port("any80", 80))
	return config
}

func TestRenderHiddenTCPPortRecords(t *testing.T) {
	services := compileServices(portRoutesConfig(), "198.51.100.10")
	rendered := renderHAProxyConfig(services, "/tmp/reject.pem")
	for _, expected := range []string{
		"tcp-request content accept if { req.ssl_hello_type 1 } !{ req.ssl_sni -m found }\n",
		"use_backend be_any80 if { dst_port 80 }\n", "use_backend be_any443 if { dst_port 443 }\n",
		"backend be_any443\n", "server srv_any443-1 192.0.2.30:443 weight 1",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("config does not contain %q:\n%s", expected, rendered)
		}
	}
	order := []string{"be_exact if", "be_anyws if", "be_any80 if", "be_any443 if", "be_ezhiklb.reject-tls if"}
	for i := 1; i < len(order); i++ {
		if strings.Index(rendered, order[i-1]) > strings.Index(rendered, order[i]) {
			t.Fatalf("%q must come before %q:\n%s", order[i-1], order[i], rendered)
		}
	}
	if strings.Contains(rendered, "ignored.example.org") || strings.Contains(rendered, "frontend fe_any") || strings.Contains(rendered, "tcp:443") {
		t.Fatalf("a port record leaked its SNI or got its own frontend:\n%s", rendered)
	}
	labels := map[string]uint16{}
	for _, service := range services {
		labels[service.SNI] = service.Port
	}
	if labels["tcp:443"] != 443 || labels["tcp:80"] != 80 {
		t.Fatalf("port record labels = %#v", labels)
	}
	if strings.Contains(renderHAProxyConfig(compileServices(httpRoutesConfig(), "198.51.100.10"), "/tmp/reject.pem"), "dst_port") {
		t.Fatal("a profile without port records must keep the 2.0.5 frontend")
	}
}
