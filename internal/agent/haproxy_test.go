package agent

import (
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
	for _, expected := range []string{"frontend fe_germany", "bind 198.51.100.10:443", "backend be_germany", "stick on src", "server srv_de-1 192.0.2.10:8443 weight 2"} {
		if !strings.Contains(config, expected) { t.Fatalf("config does not contain %q:\n%s", expected, config) }
	}
	if strings.Contains(config, ":53") || strings.Contains(config, "be_dns") { t.Fatalf("UDP leaked into HAProxy config:\n%s", config) }
}
