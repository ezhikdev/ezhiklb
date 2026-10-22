package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Receive steering (RPS/RFS). Most VPS NICs (virtio-net with one queue) raise
// a single RX interrupt pinned to one CPU, and without RPS the whole receive
// path — NAPI, conntrack, IPVS and the TCP stack of every HAProxy session —
// runs as softirq on that CPU while the others idle. RPS hashes flows across
// all CPUs; RFS then prefers the CPU where the HAProxy thread reading that
// socket runs, keeping caches warm.
const (
	// rpsSockFlowEntries sizes the global RFS table. One proxied TCP session
	// is two sockets, so 65536 covers ~32k busy sessions before collisions
	// only cost locality (never correctness).
	rpsSockFlowEntries = 65536
)

// Overridable by tests.
var (
	sysNetRoot      = "/sys/class/net"
	cpuOnlinePath   = "/sys/devices/system/cpu/online"
	rpsSockFlowPath = "/proc/sys/net/core/rps_sock_flow_entries"
)

// tuneReceiveSteering enables RPS/RFS on every single-queue-per-CPU interface
// whose RPS mask is still the kernel default (empty). Masks an administrator
// already set are left alone. All failures are reported but non-fatal: sysfs
// is read-only in many containers and forwarding works without steering.
func (r *Reconciler) tuneReceiveSteering() {
	cpus, err := onlineCPUs()
	if err != nil {
		r.logger.Warn("receive steering skipped", "error", err)
		return
	}
	if len(cpus) < 2 {
		return
	}
	mask := cpuMask(cpus)
	tuned := 0
	entries, err := os.ReadDir(sysNetRoot)
	if err != nil {
		r.logger.Warn("receive steering skipped", "error", err)
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		dir := filepath.Join(sysNetRoot, name)
		if name == "lo" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "bridge")); err == nil {
			// A bridge's own backlog is not where its ports' packets land.
			continue
		}
		queues, _ := filepath.Glob(filepath.Join(dir, "queues", "rx-*"))
		// Multi-queue NICs with a queue per CPU already spread interrupts.
		if len(queues) == 0 || len(queues) >= len(cpus) {
			continue
		}
		flowCount := rpsSockFlowEntries / len(queues)
		for _, queue := range queues {
			current, err := os.ReadFile(filepath.Join(queue, "rps_cpus"))
			if err != nil || !emptyCPUMask(string(current)) {
				continue
			}
			if err := os.WriteFile(filepath.Join(queue, "rps_cpus"), []byte(mask), 0); err != nil {
				r.logger.Warn("could not enable RPS", "interface", name, "queue", filepath.Base(queue), "error", err)
				continue
			}
			if err := raiseIntFile(filepath.Join(queue, "rps_flow_cnt"), flowCount); err != nil {
				r.logger.Warn("could not enable RFS for queue", "interface", name, "queue", filepath.Base(queue), "error", err)
			}
			tuned++
		}
	}
	if tuned == 0 {
		return
	}
	if err := raiseIntFile(rpsSockFlowPath, rpsSockFlowEntries); err != nil {
		// Only the host network namespace exposes this table.
		r.logger.Warn("could not size the RFS flow table", "error", err)
	}
	r.logger.Info("enabled receive packet steering", "queues", tuned, "cpus", len(cpus), "mask", mask)
}

func onlineCPUs() ([]int, error) {
	raw, err := os.ReadFile(cpuOnlinePath)
	if err != nil {
		return nil, err
	}
	return parseCPUList(strings.TrimSpace(string(raw)))
}

// parseCPUList parses the kernel's CPU list format, e.g. "0-3,6,8-9".
func parseCPUList(value string) ([]int, error) {
	var cpus []int
	for part := range strings.SplitSeq(value, ",") {
		if part == "" {
			continue
		}
		low, high, isRange := strings.Cut(part, "-")
		first, err := strconv.Atoi(low)
		if err != nil {
			return nil, fmt.Errorf("parse CPU list %q: %w", value, err)
		}
		last := first
		if isRange {
			if last, err = strconv.Atoi(high); err != nil {
				return nil, fmt.Errorf("parse CPU list %q: %w", value, err)
			}
		}
		if first < 0 || last < first || last > 8191 {
			return nil, fmt.Errorf("parse CPU list %q: invalid range %q", value, part)
		}
		for cpu := first; cpu <= last; cpu++ {
			cpus = append(cpus, cpu)
		}
	}
	if len(cpus) == 0 {
		return nil, errors.New("no online CPUs")
	}
	return cpus, nil
}

// cpuMask renders the bitmap format of rps_cpus: comma-separated 32-bit hex
// words, most significant first ("f", "ff,ffffffff").
func cpuMask(cpus []int) string {
	highest := 0
	for _, cpu := range cpus {
		highest = max(highest, cpu)
	}
	words := make([]uint32, highest/32+1)
	for _, cpu := range cpus {
		words[cpu/32] |= 1 << (cpu % 32)
	}
	parts := make([]string, len(words))
	for i, word := range words {
		format := "%08x"
		if i == len(words)-1 {
			format = "%x"
		}
		parts[len(words)-1-i] = fmt.Sprintf(format, word)
	}
	return strings.Join(parts, ",")
}

func emptyCPUMask(value string) bool {
	return strings.Trim(strings.TrimSpace(value), "0,") == ""
}

// raiseIntFile writes value unless the file already holds a larger one.
func raiseIntFile(path string, value int) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if current, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && current >= value {
		return nil
	}
	return os.WriteFile(path, []byte(strconv.Itoa(value)), 0)
}
