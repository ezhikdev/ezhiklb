package domain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultProfileIsValid(t *testing.T) {
	if err := DefaultProfileConfig().Validate(); err != nil {
		t.Fatalf("default profile is invalid: %v", err)
	}
}

func TestDualProtocolListenerIsValid(t *testing.T) {
	config := DefaultProfileConfig()
	config.Listeners = []Listener{{
		ID: "listener", Name: "VPN", Enabled: true, ListenAddress: "0.0.0.0", ListenPort: 8002,
		Protocols: []Protocol{ProtocolTCP, ProtocolUDP}, Scheduler: "wrr",
		Backends: []Backend{{ID: "backend", Address: "192.0.2.10", Port: 8080, Weight: 2, Enabled: true}},
	}}
	if err := config.Validate(); err != nil {
		t.Fatalf("dual protocol profile is invalid: %v", err)
	}
}

func TestDuplicateProtocolServiceIsRejected(t *testing.T) {
	config := DefaultProfileConfig()
	listener := Listener{ID: "a", Name: "A", Enabled: true, ListenAddress: "0.0.0.0", ListenPort: 8002, Protocols: []Protocol{ProtocolUDP}, Scheduler: "wrr", Backends: []Backend{{ID: "a", Address: "192.0.2.1", Port: 8002, Weight: 1, Enabled: true}}}
	other := listener
	other.ID, other.Name = "b", "B"
	config.Listeners = []Listener{listener, other}
	if err := config.Validate(); err == nil {
		t.Fatal("expected a duplicate service validation error")
	}
}

func TestParseLegacyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.conf")
	data := "[GLOBAL]\nPERSISTENCE=600\nSCHEDULER=wrr\n[DE]\nPORT=8002\nENABLED=1\nSERVER=192.0.2.10 WEIGHT=2\nSERVER=192.0.2.11 WEIGHT=1\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := ParseLegacyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(config.Listeners); got != 1 {
		t.Fatalf("listeners = %d, want 1", got)
	}
	listener := config.Listeners[0]
	if listener.ListenPort != 8002 || listener.AffinitySecs != 600 || len(listener.Backends) != 2 {
		t.Fatalf("unexpected migrated listener: %#v", listener)
	}
	if listener.Backends[0].Port != 8002 || listener.Backends[0].Weight != 2 {
		t.Fatalf("unexpected migrated backend: %#v", listener.Backends[0])
	}
}

