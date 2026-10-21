package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

// Hidden profiles share one HAProxy frontend on domain.HiddenPorts. A dot can
// never appear in safeHAProxyName output, so these names cannot collide with
// a record's own fe_/be_ proxies.
const (
	sniFrontendName      = "fe_ezhiklb.sni"
	sniRejectTLSBackend  = "be_ezhiklb.reject-tls"
	sniRejectHTTPBackend = "be_ezhiklb.reject-plain"
	sniRejectTLSSocket   = "abns@ezhiklb-sni-reject-tls"
	sniRejectHTTPSocket  = "abns@ezhiklb-sni-reject-plain"
	// sniInspectDelay bounds how long a client may take to send its first
	// bytes. TCP connections are accepted immediately, so a plain connect
	// (availability probe) always succeeds.
	sniInspectDelay = "5s"
	inputChain      = "EZHIKLB-INPUT"
)

func hiddenServices(services []Service) []Service {
	result := make([]Service, 0)
	for _, service := range services {
		if service.SNI != "" {
			result = append(result, service)
		}
	}
	return result
}

// sniRoutingOrder puts exact names first and wildcards after them, longest
// suffix first, so the most specific record always wins.
func sniRoutingOrder(services []Service) []Service {
	ordered := append([]Service(nil), services...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i].SNI, ordered[j].SNI
		leftWildcard, rightWildcard := strings.HasPrefix(left, "*."), strings.HasPrefix(right, "*.")
		if leftWildcard != rightWildcard {
			return !leftWildcard
		}
		if leftWildcard && len(left) != len(right) {
			return len(left) > len(right)
		}
		return left < right
	})
	return ordered
}

func sniCondition(name string) string {
	if suffix, wildcard := strings.CutPrefix(name, "*."); wildcard {
		return "{ req.ssl_sni -m end -i ." + suffix + " }"
	}
	return "{ req.ssl_sni -i " + name + " }"
}

// renderSNIFrontend writes the shared hidden-profile entry point. Matching
// connections go to the record's backend; TLS without a matching SNI gets a
// TLS alert from a strict-sni listener, anything else an HTTP 400.
func renderSNIFrontend(b *strings.Builder, services []Service, rejectCertificate string) {
	address := services[0].Address
	fmt.Fprintf(b, "frontend %s\n", sniFrontendName)
	for _, port := range domain.HiddenPorts {
		fmt.Fprintf(b, "  bind %s:%d\n", address, port)
	}
	// Wait for the complete ClientHello: large post-quantum hellos span
	// several TCP segments and req.ssl_sni reports "may change" until then.
	fmt.Fprintf(b, "  tcp-request inspect-delay %s\n", sniInspectDelay)
	b.WriteString("  tcp-request content accept if { req.ssl_sni -m found }\n")
	b.WriteString("  tcp-request content accept if { req.len gt 0 } !{ req.ssl_hello_type 1 }\n")
	for _, service := range sniRoutingOrder(services) {
		fmt.Fprintf(b, "  use_backend be_%s if %s\n", safeHAProxyName(service.ListenerID), sniCondition(service.SNI))
	}
	fmt.Fprintf(b, "  use_backend %s if { req.ssl_hello_type 1 }\n", sniRejectTLSBackend)
	fmt.Fprintf(b, "  default_backend %s\n\n", sniRejectHTTPBackend)

	fmt.Fprintf(b, "backend %s\n  server reject %s\n\n", sniRejectTLSBackend, sniRejectTLSSocket)
	fmt.Fprintf(b, "backend %s\n  server reject %s\n\n", sniRejectHTTPBackend, sniRejectHTTPSocket)
	// strict-sni aborts every handshake: the placeholder certificate's random
	// name is never requested, and no certificate content is ever sent.
	fmt.Fprintf(b, "frontend fe_ezhiklb.reject-tls\n  mode http\n  no log\n  bind %s ssl crt %s strict-sni\n  timeout http-request 5s\n  http-request deny deny_status 421\n\n", sniRejectTLSSocket, rejectCertificate)
	// http-ignore-probes closes silent connections without a 408 response,
	// so a bare TCP probe learns nothing beyond "port open". A browser opening
	// http:// for a record's domain is sent to https:// (same host and path);
	// any other Host still gets 400.
	fmt.Fprintf(b, "frontend fe_ezhiklb.reject-plain\n  mode http\n  no log\n  option http-ignore-probes\n  bind %s\n  timeout http-request 5s\n", sniRejectHTTPSocket)
	for _, service := range sniRoutingOrder(services) {
		fmt.Fprintf(b, "  http-request redirect scheme https code 301 if %s\n", hostCondition(service.SNI))
	}
	b.WriteString("  http-request deny deny_status 400\n\n")
}

// hostCondition matches the HTTP Host header (without a port) like sniCondition.
func hostCondition(name string) string {
	if suffix, wildcard := strings.CutPrefix(name, "*."); wildcard {
		return "{ hdr(host),field(1,:) -m end -i ." + suffix + " }"
	}
	return "{ hdr(host),field(1,:) -i " + name + " }"
}

func (r *Reconciler) sniRejectCertificatePath() string {
	return filepath.Join(filepath.Dir(r.statePath), "sni-reject.pem")
}

// ensureRejectCertificate keeps a self-signed placeholder for the strict-sni
// reject listener. HAProxy needs a certificate to parse a TLS bind line.
func ensureRejectCertificate(path string) error {
	if data, err := os.ReadFile(path); err == nil {
		if _, parseErr := tls.X509KeyPair(data, data); parseErr == nil {
			return nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	label := make([]byte, 8)
	if _, err := rand.Read(label); err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return err
	}
	name := hex.EncodeToString(label) + ".invalid"
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(20, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	data := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})...)
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// socketEndpoint is one listening socket a set of services needs.
type socketEndpoint struct {
	protocol domain.Protocol
	address  string
	port     uint16
}

func (e socketEndpoint) String() string { return net.JoinHostPort(e.address, strconv.Itoa(int(e.port))) }

// serviceEndpoints expands hidden records to every port of the shared SNI
// frontend; a regular service owns exactly its own socket.
func serviceEndpoints(services []Service) []socketEndpoint {
	seen := map[socketEndpoint]bool{}
	result := make([]socketEndpoint, 0, len(services))
	add := func(endpoint socketEndpoint) {
		if !seen[endpoint] {
			seen[endpoint] = true
			result = append(result, endpoint)
		}
	}
	for _, service := range services {
		if service.SNI != "" {
			for _, port := range domain.HiddenPorts {
				add(socketEndpoint{protocol: domain.ProtocolTCP, address: service.Address, port: port})
			}
			continue
		}
		add(socketEndpoint{protocol: service.Protocol, address: service.Address, port: service.Port})
	}
	return result
}

// Inbound ports a hidden node keeps open in addition to what is detected.
var baseHiddenInputPorts = []uint16{22, 80, 443}

// Processes whose listening TCP ports must stay reachable on a hidden node:
// SSH servers (custom ports included) and a co-located EzhikLB panel, whose
// agent API remote nodes depend on.
var essentialInputProcesses = []string{"sshd", "dropbear", "ezhiklb"}

// detectInputPorts returns the TCP ports a hidden node must keep open.
func (r *Reconciler) detectInputPorts(ctx context.Context) ([]uint16, error) {
	output, err := r.runner.Run(ctx, "ss", []string{"-H", "-lntup"}, "")
	if err != nil {
		return nil, fmt.Errorf("inspect listening services: %w", err)
	}
	ports := append([]uint16(nil), baseHiddenInputPorts...)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || strings.ToLower(fields[0]) != "tcp" {
			continue
		}
		essential := false
		for _, process := range essentialInputProcesses {
			if strings.Contains(line, `(("`+process+`",`) || strings.Contains(line, `),("`+process+`",`) {
				essential = true
				break
			}
		}
		if !essential {
			continue
		}
		host, port, splitErr := net.SplitHostPort(fields[4])
		if splitErr != nil {
			continue
		}
		if address, parseErr := netip.ParseAddr(strings.Trim(host, "[]")); parseErr == nil && address.IsLoopback() {
			continue
		}
		if value, parseErr := strconv.ParseUint(port, 10, 16); parseErr == nil && value > 0 {
			ports = append(ports, uint16(value))
		}
	}
	return normalizePorts(ports), nil
}

func normalizePorts(ports []uint16) []uint16 {
	result := append([]uint16(nil), ports...)
	slices.Sort(result)
	return slices.Compact(result)
}

func multiportChunks(ports []uint16) []string {
	var chunks []string
	// iptables multiport accepts at most 15 ports per rule.
	for start := 0; start < len(ports); start += 15 {
		end := min(start+15, len(ports))
		values := make([]string, 0, end-start)
		for _, port := range ports[start:end] {
			values = append(values, strconv.Itoa(int(port)))
		}
		chunks = append(chunks, strings.Join(values, ","))
	}
	return chunks
}

// renderInputRules builds the EZHIKLB-INPUT chain. Allowed traffic RETURNs
// to the host's own INPUT rules (which may restrict it further); the chain
// only ever narrows what the host accepts and never opens anything.
func renderInputRules(ports []uint16, ipv6 bool) string {
	lines := []string{"*filter", ":" + inputChain + " - [0:0]", "-F " + inputChain,
		"-A " + inputChain + " -i lo -j RETURN",
		"-A " + inputChain + " -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN"}
	if ipv6 {
		lines = append(lines,
			"-A "+inputChain+" -p ipv6-icmp -j RETURN",
			"-A "+inputChain+" -p udp --sport 547 --dport 546 -j RETURN")
	} else {
		lines = append(lines,
			"-A "+inputChain+" -p icmp -j RETURN",
			"-A "+inputChain+" -p udp --sport 67 --dport 68 -j RETURN")
	}
	for _, chunk := range multiportChunks(ports) {
		lines = append(lines, "-A "+inputChain+" -p tcp -m multiport --dports "+chunk+" -j RETURN")
	}
	lines = append(lines, "-A "+inputChain+" -j DROP", "COMMIT", "")
	return strings.Join(lines, "\n")
}

// applyInputFirewall closes every inbound port except the given TCP ports
// and system traffic. IPv6 is handled best-effort: a host without a usable
// ip6tables keeps its IPv4 protection and reports the IPv6 problem.
func (r *Reconciler) applyInputFirewall(ctx context.Context, ports []uint16) error {
	if _, err := r.runner.Run(ctx, "iptables-restore", []string{"--noflush"}, renderInputRules(ports, false)); err != nil {
		return err
	}
	if err := r.ensureJumpWith(ctx, "iptables", "filter", "INPUT", inputChain); err != nil {
		return err
	}
	if _, err := r.runner.Run(ctx, "ip6tables-restore", []string{"--noflush"}, renderInputRules(ports, true)); err != nil {
		r.logger.Warn("IPv6 inbound firewall for the hidden profile was not applied", "error", err)
		return nil
	}
	if err := r.ensureJumpWith(ctx, "ip6tables", "filter", "INPUT", inputChain); err != nil {
		r.logger.Warn("IPv6 inbound firewall jump was not installed", "error", err)
	}
	return nil
}

func (r *Reconciler) removeInputFirewall(ctx context.Context) error {
	if err := r.removeManagedChainWith(ctx, "iptables", "filter", "INPUT", inputChain); err != nil {
		return err
	}
	if err := r.removeManagedChainWith(ctx, "ip6tables", "filter", "INPUT", inputChain); err != nil {
		r.logger.Warn("IPv6 inbound firewall cleanup was incomplete", "error", err)
	}
	return nil
}

// inputFirewallActive reports whether the IPv4 chain and its INPUT jump exist.
func (r *Reconciler) inputFirewallActive(ctx context.Context) bool {
	if _, err := r.runner.Run(ctx, "iptables", []string{"-w", "5", "-C", "INPUT", "-j", inputChain}, ""); err != nil {
		return false
	}
	output, err := r.runner.Run(ctx, "iptables", []string{"-w", "5", "-S", inputChain}, "")
	return err == nil && strings.Contains(output, "-j DROP")
}

// EnsureInputFirewall re-asserts a hidden node's inbound firewall. It picks
// up SSH or panel port changes and repairs the INPUT jump if another tool
// (ufw, a firewall reload) removed it. Regular nodes are never touched.
func (r *Reconciler) EnsureInputFirewall(ctx context.Context) (domain.NodeDiagnostics, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result domain.NodeDiagnostics
	state, err := r.loadState()
	if err != nil || !state.Hidden {
		return result, err
	}
	result.HiddenMode = true
	ports, err := r.detectInputPorts(ctx)
	if err != nil {
		result.AllowedInputPorts = state.InputPorts
		result.InputFirewallReady = r.inputFirewallActive(ctx)
		return result, err
	}
	if !slices.Equal(ports, state.InputPorts) || !r.inputFirewallActive(ctx) {
		r.logger.Info("re-applying the hidden profile inbound firewall", "ports", ports)
		if err := r.applyInputFirewall(ctx, ports); err != nil {
			result.AllowedInputPorts = state.InputPorts
			return result, fmt.Errorf("apply inbound firewall: %w", err)
		}
		state.InputPorts = ports
		if err := r.saveState(state); err != nil {
			return result, err
		}
	}
	result.AllowedInputPorts = ports
	result.InputFirewallReady = r.inputFirewallActive(ctx)
	if !result.InputFirewallReady {
		return result, errors.New("hidden profile inbound firewall is not active")
	}
	return result, nil
}
