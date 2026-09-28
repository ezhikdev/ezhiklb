package agent

import (
	"context"
	"encoding/csv"
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

func renderHAProxyConfig(services []Service) string {
	var b strings.Builder
	b.WriteString("global\n  log stdout format raw local0\n  user haproxy\n  group haproxy\n  stats socket " + haProxySocket + " user root group root mode 600 level admin\n\ndefaults\n  mode tcp\n  log global\n  option tcplog\n  timeout connect 5s\n  timeout client 1h\n  timeout server 1h\n  timeout check 3s\n\n")
	for _, service := range onlyProtocol(services, domain.ProtocolTCP) {
		name := safeHAProxyName(service.ListenerID)
		fmt.Fprintf(&b, "frontend fe_%s\n  bind %s:%d\n  default_backend be_%s\n\n", name, service.Address, service.Port, name)
		fmt.Fprintf(&b, "backend be_%s\n  balance %s\n", name, map[string]string{"rr":"roundrobin", "wrr":"roundrobin"}[service.Scheduler])
		if service.AffinitySecs > 0 {
			fmt.Fprintf(&b, "  stick-table type ip size 1m expire %ds\n  stick on src\n", service.AffinitySecs)
		}
		for _, destination := range service.Destinations {
			fmt.Fprintf(&b, "  server srv_%s %s:%d weight %d\n", safeHAProxyName(destination.ID), destination.Address, destination.Port, haProxyWeight(service,destination.Weight))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func haProxyWeight(service Service, weight int) int {
	if weight <= 0 { return 0 }
	maximum := weight
	for _, destination := range service.Destinations { if destination.Weight > maximum { maximum=destination.Weight } }
	if maximum <= 256 { return weight }
	normalized := weight*256/maximum
	if normalized < 1 { return 1 }
	return normalized
}

func (r *Reconciler) haProxyConfigPath() string { return filepath.Join(filepath.Dir(r.statePath), "haproxy.cfg") }

func (r *Reconciler) applyHAProxy(ctx context.Context, services []Service) error {
	config := renderHAProxyConfig(services)
	path := r.haProxyConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil { return err }
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(config), 0640); err != nil { return err }
	if _, err := r.runner.Run(ctx, "haproxy", []string{"-c", "-f", tmp}, ""); err != nil { _ = os.Remove(tmp); return fmt.Errorf("validate HAProxy config: %w", err) }
	if err := os.Rename(tmp, path); err != nil { return err }
	if len(onlyProtocol(services, domain.ProtocolTCP)) == 0 {
		_, err := r.runner.Run(ctx, "systemctl", []string{"stop", "ezhiklb-haproxy.service"}, "")
		return err
	}
	if _, err := r.runner.Run(ctx, "systemctl", []string{"reload-or-restart", "ezhiklb-haproxy.service"}, ""); err != nil { return fmt.Errorf("reload HAProxy: %w", err) }
	return nil
}

func sendHAProxyCommand(command string) (string, error) {
	conn, err := net.DialTimeout("unix", haProxySocket, 2*time.Second)
	if err != nil { return "", err }
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3*time.Second))
	if _, err := fmt.Fprintln(conn, command); err != nil { return "", err }
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
	output, err := sendHAProxyCommand("show stat")
	if err != nil { return nil, err }
	reader := csv.NewReader(strings.NewReader(output))
	records, err := reader.ReadAll()
	if err != nil || len(records) == 0 { return nil, err }
	headers := records[0]
	index := map[string]int{}
	for i, header := range headers { index[strings.TrimPrefix(header, "# ")] = i }
	value := func(row []string, key string) string { if i, ok := index[key]; ok && i < len(row) { return row[i] }; return "" }
	parse := func(row []string, key string) uint64 { n, _ := strconv.ParseUint(value(row, key), 10, 64); return n }
	now := time.Now().UTC()
	result := make([]domain.ServiceStat, 0)
	byBackend := map[string]Service{}
	for _, service := range onlyProtocol(services, domain.ProtocolTCP) { byBackend["be_"+safeHAProxyName(service.ListenerID)] = service }
	for _, row := range records[1:] {
		service, ok := byBackend[value(row, "pxname")]
		if !ok { continue }
		sv := value(row, "svname")
		if sv != "BACKEND" && !strings.HasPrefix(sv, "srv_") { continue }
		stat := domain.ServiceStat{Protocol: domain.ProtocolTCP, ListenAddress: service.Address, ListenPort: service.Port, Connections: parse(row, "stot"), IncomingBytes: parse(row, "bin"), OutgoingBytes: parse(row, "bout"), CollectedAt: now}
		if sv != "BACKEND" {
			for _, destination := range service.Destinations {
				if sv == "srv_"+safeHAProxyName(destination.ID) { stat.BackendAddress, stat.BackendPort = destination.Address, destination.Port; break }
			}
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
	output, err := sendHAProxyCommand("show sess")
	if err != nil { return nil }
	unique := map[string]struct{}{}
	for _, field := range strings.Fields(output) {
		if !strings.HasPrefix(field, "src=") { continue }
		value := strings.TrimPrefix(strings.TrimSuffix(field, "]"), "src=")
		if host, _, splitErr := net.SplitHostPort(value); splitErr == nil { unique[host] = struct{}{} }
	}
	result := make([]string,0,len(unique)); for address := range unique { result=append(result,address) }; return result
}

