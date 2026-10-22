package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain keeps every Reconcile-driven test away from the real sysfs, which
// the tests could otherwise retune when run as root.
func TestMain(m *testing.M) {
	sysNetRoot = filepath.Join(os.TempDir(), "ezhiklb-test-no-sysfs")
	os.Exit(m.Run())
}

func TestParseCPUListAndMask(t *testing.T) {
	cases := map[string]string{
		"0":         "1",
		"0-3":       "f",
		"0-1,3":     "b",
		"0-39":      "ff,ffffffff",
		"0-31":      "ffffffff",
		"32":        "1,00000000",
		"0-1,64-65": "3,00000000,00000003",
	}
	for list, want := range cases {
		cpus, err := parseCPUList(list)
		if err != nil {
			t.Fatalf("%s: %v", list, err)
		}
		if got := cpuMask(cpus); got != want {
			t.Errorf("cpuMask(%s) = %s, want %s", list, got, want)
		}
	}
	for _, bad := range []string{"", "x", "3-1", "-1"} {
		if _, err := parseCPUList(bad); err == nil {
			t.Errorf("parseCPUList(%q) accepted", bad)
		}
	}
}

func writeFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}

func TestTuneReceiveSteering(t *testing.T) {
	root := t.TempDir()
	oldNet, oldCPU, oldFlow := sysNetRoot, cpuOnlinePath, rpsSockFlowPath
	t.Cleanup(func() { sysNetRoot, cpuOnlinePath, rpsSockFlowPath = oldNet, oldCPU, oldFlow })
	sysNetRoot = filepath.Join(root, "net")
	cpuOnlinePath = filepath.Join(root, "online")
	rpsSockFlowPath = filepath.Join(root, "rps_sock_flow_entries")
	writeFile(t, cpuOnlinePath, "0-3\n")
	writeFile(t, rpsSockFlowPath, "0\n")
	queue := func(dev string, n int) string { return filepath.Join(sysNetRoot, dev, "queues", "rx-"+string(rune('0'+n))) }
	// Single-queue VPS NIC: must be tuned.
	writeFile(t, filepath.Join(queue("enp1s0", 0), "rps_cpus"), "0\n")
	writeFile(t, filepath.Join(queue("enp1s0", 0), "rps_flow_cnt"), "0\n")
	// Administrator's own mask: must be kept.
	writeFile(t, filepath.Join(queue("eth1", 0), "rps_cpus"), "c\n")
	writeFile(t, filepath.Join(queue("eth1", 0), "rps_flow_cnt"), "0\n")
	// A queue per CPU: hardware already spreads the load.
	for i := range 4 {
		writeFile(t, filepath.Join(queue("mq0", i), "rps_cpus"), "0\n")
		writeFile(t, filepath.Join(queue("mq0", i), "rps_flow_cnt"), "0\n")
	}
	// Loopback and bridges are skipped.
	writeFile(t, filepath.Join(queue("lo", 0), "rps_cpus"), "0\n")
	writeFile(t, filepath.Join(queue("br0", 0), "rps_cpus"), "0\n")
	writeFile(t, filepath.Join(sysNetRoot, "br0", "bridge", "stp_state"), "0\n")

	r := NewReconciler(&fakeRunner{}, filepath.Join(root, "state.json"), nil)
	r.tuneReceiveSteering()

	if got := readFile(t, filepath.Join(queue("enp1s0", 0), "rps_cpus")); got != "f" {
		t.Fatalf("enp1s0 rps_cpus = %q, want f", got)
	}
	if got := readFile(t, filepath.Join(queue("enp1s0", 0), "rps_flow_cnt")); got != "65536" {
		t.Fatalf("enp1s0 rps_flow_cnt = %q", got)
	}
	if got := readFile(t, rpsSockFlowPath); got != "65536" {
		t.Fatalf("rps_sock_flow_entries = %q", got)
	}
	for _, check := range [][2]string{
		{filepath.Join(queue("eth1", 0), "rps_cpus"), "c"},
		{filepath.Join(queue("eth1", 0), "rps_flow_cnt"), "0"},
		{filepath.Join(queue("mq0", 2), "rps_cpus"), "0"},
		{filepath.Join(queue("lo", 0), "rps_cpus"), "0"},
		{filepath.Join(queue("br0", 0), "rps_cpus"), "0"},
	} {
		if got := readFile(t, check[0]); got != check[1] {
			t.Errorf("%s = %q, want unchanged %q", check[0], got, check[1])
		}
	}

	// A larger global table set by the administrator is never lowered.
	writeFile(t, rpsSockFlowPath, "131072\n")
	writeFile(t, filepath.Join(queue("enp1s0", 0), "rps_cpus"), "0\n")
	r.tuneReceiveSteering()
	if got := readFile(t, rpsSockFlowPath); got != "131072" {
		t.Fatalf("rps_sock_flow_entries lowered to %q", got)
	}
}
