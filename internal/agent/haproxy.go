package agent

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

const haProxySocket = "/run/ezhiklb-haproxy/admin.sock"

// haProxyMaxConn bounds concurrent TCP sessions per node (~35 MB per process).
const haProxyMaxConn = 200000

func onlyProtocol(services []Service, protocol domain.Protocol) []Service {
	result := make([]Service, 0)
	for _, service := range services {
		if service.Protocol == protocol { result = append(result, service) }
	}
	return result
}

func safeHAProxyName(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' { b.WriteRune(r) } else { b.WriteByte('_') }
	}
	if b.Len() == 0 { return "unnamed" }
	return b.String()
}

// renderHAProxyConfig renders every TCP service. rejectCertificate is the
// placeholder used by the hidden-profile TLS reject listener; it is only
// referenced when SNI services are present.
func renderHAProxyConfig(services []Service, rejectCertificate string) string {
	var b strings.Builder
	// Let HAProxy choose kernel splicing for bulk TCP streams. Session logs,
	// counters, weights and source affinity still use the same L4 proxy path.
	// An explicit maxconn sizes HAProxy's file-descriptor tables. Derived from
	// the unit's LimitNOFILE=1048576 it costs ~70 MB per process, and every
	// worker generation still draining long VPN sessions after a reload keeps
	// its own copy — enough to exhaust a 1 GB node.
	b.WriteString("global\n  log stdout format raw local0\n  user haproxy\n  group haproxy\n  maxconn " + strconv.Itoa(haProxyMaxConn) + "\n  stats socket " + haProxySocket + " user root group root mode 600 level admin\n\ndefaults\n  mode tcp\n  log global\n  option tcplog\n  option splice-auto\n  timeout connect 5s\n  timeout client 1h\n  timeout server 1h\n  timeout check 3s\n\n")
	tcp := onlyProtocol(services, domain.ProtocolTCP)
	if hidden := hiddenServices(tcp); len(hidden) > 0 {
		renderSNIFrontend(&b, hidden, rejectCertificate)
	}
	for _, service := range tcp {
		name := safeHAProxyName(service.ListenerID)
		if service.SNI == "" {
			fmt.Fprintf(&b, "frontend fe_%s\n  bind %s:%d\n  default_backend be_%s\n\n", name, service.Address, service.Port, name)
		}
		fmt.Fprintf(&b, "backend be_%s\n  balance %s\n", name, map[string]string{"rr":"roundrobin", "wrr":"roundrobin"}[service.Scheduler])
		if service.AffinitySecs > 0 {
			fmt.Fprintf(&b, "  stick-table type ip size 1m expire %ds\n  stick on src\n", service.AffinitySecs)
		}
		maximum := 0
		for _, destination := range service.Destinations { maximum = max(maximum, destination.Weight) }
		for _, destination := range service.Destinations {
			fmt.Fprintf(&b, "  server srv_%s %s:%d weight %d\n", safeHAProxyName(destination.ID), destination.Address, destination.Port, normalizedHAProxyWeight(maximum,destination.Weight))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func haProxyWeight(service Service, weight int) int {
	if weight <= 0 { return 0 }
	maximum := weight
	for _, destination := range service.Destinations { if destination.Weight > maximum { maximum=destination.Weight } }
	return normalizedHAProxyWeight(maximum, weight)
}

func normalizedHAProxyWeight(maximum, weight int) int {
	if weight <= 0 { return 0 }
	if maximum <= 256 { return weight }
	normalized := weight*256/maximum
	if normalized < 1 { return 1 }
	return normalized
}

func (r *Reconciler) haProxyConfigPath() string { return filepath.Join(filepath.Dir(r.statePath), "haproxy.cfg") }

func (r *Reconciler) applyHAProxy(ctx context.Context, services []Service) error {
	path := r.haProxyConfigPath()
	if len(onlyProtocol(services, domain.ProtocolTCP)) == 0 {
		if _, err := r.runner.Run(ctx, "systemctl", []string{"stop", "ezhiklb-haproxy.service"}, ""); err != nil {
			return err
		}
		// HAProxy can reject a configuration with no listeners. Stop first
		// and remove its managed config instead of validating an empty one.
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	rejectCertificate := r.sniRejectCertificatePath()
	if len(hiddenServices(services)) > 0 {
		if err := ensureRejectCertificate(rejectCertificate); err != nil { return fmt.Errorf("prepare SNI reject certificate: %w", err) }
	}
	config := renderHAProxyConfig(services, rejectCertificate)
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil { return err }
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(config), 0640); err != nil { return err }
	if _, err := r.runner.Run(ctx, "haproxy", []string{"-c", "-f", tmp}, ""); err != nil { _ = os.Remove(tmp); return fmt.Errorf("validate HAProxy config: %w", err) }
	if err := os.Rename(tmp, path); err != nil { return err }
	if _, err := r.runner.Run(ctx, "systemctl", []string{"reload-or-restart", "ezhiklb-haproxy.service"}, ""); err != nil { return fmt.Errorf("reload HAProxy: %w", err) }
	return nil
}

func openHAProxyCommand(command string) (net.Conn, error) {
	conn, err := net.DialTimeout("unix", haProxySocket, 2*time.Second)
	if err != nil { return nil, err }
	_ = conn.SetDeadline(time.Now().Add(3*time.Second))
	if _, err := fmt.Fprintln(conn, command); err != nil { conn.Close(); return nil, err }
	return conn, nil
}

func sendHAProxyCommand(command string) (string, error) {
	conn, err := openHAProxyCommand(command)
	if err != nil { return "", err }
	defer conn.Close()
	var b strings.Builder
	if _, err := io.Copy(&b, conn); err != nil { return "", err }
	return b.String(), nil
}

func setHAProxyServerWeight(service Service, destination Destination, weight int) error {
	_, err := sendHAProxyCommand(fmt.Sprintf("set server be_%s/srv_%s weight %d", safeHAProxyName(service.ListenerID), safeHAProxyName(destination.ID), haProxyWeight(service,weight)))
	return err
}

// CollectHAProxyStats translates HAProxy's CSV counters into the same
// service/destination shape used by IPVS telemetry.
func CollectHAProxyStats(services []Service) ([]domain.ServiceStat, error) {
	conn, err := openHAProxyCommand("show stat")
	if err != nil { return nil, err }
	defer conn.Close()
	return parseHAProxyStats(conn, services)
}

func parseHAProxyStats(input io.Reader, services []Service) ([]domain.ServiceStat, error) {
	reader := csv.NewReader(input)
	headers, err := reader.Read()
	if err == io.EOF { return nil, nil }
	if err != nil { return nil, err }
	reader.ReuseRecord = true
	index := map[string]int{}
	for i, header := range headers { index[strings.TrimPrefix(header, "# ")] = i }
	value := func(row []string, key string) string { if i, ok := index[key]; ok && i < len(row) { return row[i] }; return "" }
	parse := func(row []string, key string) uint64 { n, _ := strconv.ParseUint(value(row, key), 10, 64); return n }
	now := time.Now().UTC()
	result := make([]domain.ServiceStat, 0)
	byBackend := map[string]Service{}
	byServer := map[string]map[string]Destination{}
	for _, service := range services {
		if service.Protocol != domain.ProtocolTCP { continue }
		backend := "be_"+safeHAProxyName(service.ListenerID)
		byBackend[backend] = service
		byServer[backend] = make(map[string]Destination, len(service.Destinations))
		for _, destination := range service.Destinations { byServer[backend]["srv_"+safeHAProxyName(destination.ID)] = destination }
	}
	for {
		row, err := reader.Read()
		if err == io.EOF { break }
		if err != nil { return nil, err }
		service, ok := byBackend[value(row, "pxname")]
		if !ok { continue }
		sv := value(row, "svname")
		if sv != "BACKEND" && !strings.HasPrefix(sv, "srv_") { continue }
		stat := domain.ServiceStat{Protocol: domain.ProtocolTCP, ListenAddress: service.Address, ListenPort: service.Port, SNI: service.SNI, Connections: parse(row, "stot"), IncomingBytes: parse(row, "bin"), OutgoingBytes: parse(row, "bout"), CollectedAt: now}
		if sv != "BACKEND" {
			destination, ok := byServer[value(row, "pxname")][sv]
			if !ok { continue }
			stat.BackendAddress, stat.BackendPort = destination.Address, destination.Port
		}
		result = append(result, stat)
	}
	return result, nil
}

func CollectDataPlaneStats(ctx context.Context, runner Runner, services []Service) ([]domain.ServiceStat, error) {
	result := make([]domain.ServiceStat, 0)
	udp, udpErr := CollectIPVSStats(ctx, runner)
	if udpErr == nil { for _, stat := range udp { if stat.Protocol == domain.ProtocolUDP { result = append(result, stat) } } }
	tcp, tcpErr := CollectHAProxyStats(services)
	if tcpErr == nil { result = append(result, tcp...) }
	if udpErr != nil && tcpErr != nil { return result, fmt.Errorf("IPVS: %v; HAProxy: %v", udpErr, tcpErr) }
	return result, nil
}

func readActiveHAProxyClients() []string {
	conn, err := openHAProxyCommand("show sess")
	if err != nil { return nil }
	defer conn.Close()
	clients, err := parseActiveHAProxyClients(conn)
	if err != nil { return nil }
	return clients
}

func parseActiveHAProxyClients(input io.Reader) ([]string, error) {
	unique := map[string]struct{}{}
	scanner := bufio.NewScanner(input)
	// Default show sess emits one summary line per session, not payloads.
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		for field := range strings.FieldsSeq(scanner.Text()) {
			if !strings.HasPrefix(field, "src=") { continue }
			value := strings.TrimPrefix(strings.TrimSuffix(field, "]"), "src=")
			if host, _, splitErr := net.SplitHostPort(value); splitErr == nil {
				if _, exists := unique[host]; !exists { unique[strings.Clone(host)] = struct{}{} }
			}
			break
		}
	}
	if err := scanner.Err(); err != nil { return nil, err }
	result := make([]string,0,len(unique)); for address := range unique { result=append(result,address) }; return result, nil
}

