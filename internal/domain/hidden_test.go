package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func hiddenListener(id, sni string) Listener {
	return Listener{
		ID: id, Name: id, Enabled: true, ListenAddress: "0.0.0.0", ListenPort: 443,
		Protocols: []Protocol{ProtocolTCP}, Scheduler: "wrr", SNI: sni,
		Backends: []Backend{{ID: id + "-b", Address: "192.0.2.10", Port: 8443, Weight: 1, Enabled: true}},
	}
}

func TestValidSNI(t *testing.T) {
	for _, name := range []string{"vpn.example.com", "VPN.Example.COM", "a-b.example.co.uk", "*.example.com", "xn--80ak6aa92e.xn--p1ai", "example.com."} {
		if !ValidSNI(name) {
			t.Errorf("%q rejected", name)
		}
	}
	for _, name := range []string{"", "localhost", "192.0.2.10", "*.com", "a..example.com", "-a.example.com", "a_b.example.com", "*.*.example.com", "ex ample.com", "example.123", strings.Repeat("a", 64) + ".com"} {
		if ValidSNI(name) {
			t.Errorf("%q accepted", name)
		}
	}
	if NormalizeSNI(" VPN.Example.com. ") != "vpn.example.com" {
		t.Fatal("SNI is not normalized")
	}
}

func TestHiddenProfileSharesPortsAndRoutesBySNI(t *testing.T) {
	config := DefaultProfileConfig()
	config.Hidden = true
	config.Listeners = []Listener{hiddenListener("a", "a.example.com"), hiddenListener("b", "*.example.org")}
	if err := config.Validate(); err != nil {
		t.Fatalf("records on the shared 80/443 frontend must not conflict: %v", err)
	}
	// The same records conflict in a regular profile: both claim TCP 443.
	config.Hidden = false
	if err := config.Validate(); err == nil {
		t.Fatal("regular profile accepted two listeners on TCP 443")
	}
}

func TestHiddenProfileRequiresUniqueValidSNI(t *testing.T) {
	cases := map[string][]Listener{
		"missing SNI":       {hiddenListener("a", "")},
		"invalid SNI":       {hiddenListener("a", "not a host")},
		"duplicate SNI":     {hiddenListener("a", "a.example.com"), hiddenListener("b", "A.Example.com")},
		"UDP record":        {func() Listener { l := hiddenListener("a", "a.example.com"); l.Protocols = []Protocol{ProtocolUDP}; return l }()},
		"rate limit":        {func() Listener { l := hiddenListener("a", "a.example.com"); l.RateLimitEnabled, l.RateLimitMbps = true, 100; return l }()},
		"no enabled backend": {func() Listener { l := hiddenListener("a", "a.example.com"); l.Backends[0].Enabled = false; return l }()},
	}
	for name, listeners := range cases {
		config := DefaultProfileConfig()
		config.Hidden = true
		config.Listeners = listeners
		if err := config.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestRegularProfileKeepsSNIWithoutValidatingIt(t *testing.T) {
	config := DefaultProfileConfig()
	listener := hiddenListener("a", "not a host")
	config.Listeners = []Listener{listener}
	if err := config.Validate(); err != nil {
		t.Fatalf("a dormant SNI value must not block a regular profile: %v", err)
	}
}

func TestLegacyConfigDecodesAsRegularProfile(t *testing.T) {
	var config ProfileConfig
	if err := json.Unmarshal([]byte(`{"schema_version":1,"health_check":{"enabled":true,"interval_seconds":10,"timeout_millis":1000,"failure_threshold":3,"recovery_threshold":2},"listeners":[]}`), &config); err != nil {
		t.Fatal(err)
	}
	if config.Hidden {
		t.Fatal("1.x configuration decoded as hidden")
	}
	data, _ := json.Marshal(config)
	if strings.Contains(string(data), "hidden") {
		t.Fatalf("regular profiles must keep the 1.x JSON shape: %s", data)
	}
}

func httpListener(id, host, path string) Listener {
	listener := hiddenListener(id, host)
	listener.MatchHost, listener.Path = true, path
	return listener
}

func TestHiddenHTTPRoutes(t *testing.T) {
	config := DefaultProfileConfig()
	config.Hidden = true
	// The same domain may be routed by SNI (TLS) and by Host (plain HTTP).
	config.Listeners = []Listener{hiddenListener("tls", "a.example.com"), httpListener("host", "a.example.com", ""), httpListener("both", "a.example.com", "/ws/"), httpListener("path", "", "/ws"), httpListener("wild", "*.example.com", "/")}
	if err := config.Validate(); err != nil {
		t.Fatalf("valid HTTP routes rejected: %v", err)
	}
	if !config.HasHTTPRoutes() {
		t.Fatal("HTTP routes not detected")
	}
	for listener, want := range map[int]string{0: "a.example.com", 1: "http://a.example.com", 2: "http://a.example.com/ws", 3: "http://*/ws", 4: "http://*.example.com/"} {
		if got := RouteLabel(config.Listeners[listener]); got != want {
			t.Errorf("RouteLabel(%d) = %q, want %q", listener, got, want)
		}
	}
	config.Listeners = []Listener{hiddenListener("tls", "a.example.com")}
	if config.HasHTTPRoutes() {
		t.Fatal("an SNI-only profile reports HTTP routes")
	}
}

func TestHiddenHTTPRoutesRejected(t *testing.T) {
	cases := map[string][]Listener{
		"neither host nor path": {httpListener("a", "", "")},
		"path on SNI record":    {func() Listener { l := hiddenListener("a", "a.example.com"); l.Path = "/ws"; return l }()},
		"relative path":         {httpListener("a", "", "ws")},
		"path with space":       {httpListener("a", "", "/w s")},
		"path with query":       {httpListener("a", "", "/ws?x=1")},
		"path with quote":       {httpListener("a", "", "/w's")},
		"double slash":          {httpListener("a", "", "/a//b")},
		"invalid host":          {httpListener("a", "not a host", "/ws")},
		"duplicate route":       {httpListener("a", "", "/ws"), httpListener("b", "", "/ws/")},
	}
	for name, listeners := range cases {
		config := DefaultProfileConfig()
		config.Hidden = true
		config.Listeners = listeners
		if err := config.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
