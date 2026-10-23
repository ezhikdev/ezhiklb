package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

// TCP buffer memory (net.ipv4.tcp_mem). The kernel sizes it for an ordinary
// server: roughly 9% of RAM as the hard limit. A proxy holds two sockets per
// client, and once their buffers reach the limit the kernel shrinks every
// window and prunes packets it already received. Senders retransmit, and
// clients see a few Mbit/s while the NIC, softnet and qdisc report no drops
// and the CPU idles. The tuner sizes tcp_mem from RAM, widens it while the
// kernel reports pressure and RAM allows, and re-reads RAM and vCPUs every
// check so a VPS resized without a reboot is noticed.

// Overridable by tests.
var (
	tcpMemPath    = "/proc/sys/net/ipv4/tcp_mem"
	sockstatPath  = "/proc/net/sockstat"
	netstatPath   = "/proc/net/netstat"
	meminfoPath   = "/proc/meminfo"
	vmstatPath    = "/proc/vmstat"
	psiMemoryPath = "/proc/pressure/memory"
)

const (
	// tcpMemAdaptiveStep is how far one adaptive expansion widens the hard
	// limit, in percent of RAM.
	tcpMemAdaptiveStep = 5
	// The reserve is RAM tcp_mem never reaches: the OS, HAProxy, the agent
	// and a co-located panel live there. It also has to stay available
	// before an expansion is allowed.
	tcpMemReservePercent  = 15
	tcpMemMinReserveBytes = 512 << 20
	// tcpMemPSILimit is the memory PSI "some avg10" (percent of time tasks
	// stalled on memory) above which the system counts as short of RAM.
	tcpMemPSILimit = 10.0
	tcpMemEventLimit = 20
	// tcpMemEventRepeat suppresses identical events (a condition that holds
	// for many checks) so the log and the panel list stay readable.
	tcpMemEventRepeat = 10 * time.Minute
)

type tcpTuningState struct {
	Settings domain.TCPTuning `json:"settings"`
	Adaptive int              `json:"adaptive_percent"`
	// Applied is the tcp_mem the tuner wrote last, in pages. A different
	// current value was set by someone else and is never lowered.
	Applied [3]uint64 `json:"applied_pages"`
}

// TCPMemoryTuner manages tcp_mem for one node. Tick is called once a minute.
type TCPMemoryTuner struct {
	mu            sync.Mutex
	logger        *slog.Logger
	path          string
	state         tcpTuningState
	pageSize      uint64
	ramBytes      uint64
	cpus          int
	lastPressures uint64
	lastOOM       uint64
	sampled       bool
	events        []domain.TCPMemoryEvent
	eventSeen     map[string]time.Time
	onCPUChange   func()
	now           func() time.Time
}

// NewTCPMemoryTuner loads the persisted setting from path, so a node that
// restarts while the panel is unreachable keeps the administrator's choice.
// onCPUChange runs when the number of online vCPUs changes.
func NewTCPMemoryTuner(logger *slog.Logger, path string, onCPUChange func()) *TCPMemoryTuner {
	t := &TCPMemoryTuner{logger: logger, path: path, pageSize: uint64(os.Getpagesize()), eventSeen: map[string]time.Time{}, onCPUChange: onCPUChange, now: time.Now}
	t.state.Settings = domain.DefaultTCPTuning()
	if data, err := os.ReadFile(path); err == nil {
		var saved tcpTuningState
		if json.Unmarshal(data, &saved) == nil {
			t.state = saved
			t.state.Settings = normalizeTCPTuning(saved.Settings)
		}
	}
	return t
}

func normalizeTCPTuning(settings domain.TCPTuning) domain.TCPTuning {
	if settings.MemoryPercent == 0 {
		settings.MemoryPercent = domain.TCPMemoryDefaultPercent
	}
	settings.MemoryPercent = min(max(settings.MemoryPercent, domain.TCPMemoryMinPercent), domain.TCPMemoryMaxPercent)
	return settings
}

// Settings is the setting in effect; the agent's desired-state ETag uses it.
func (t *TCPMemoryTuner) Settings() domain.TCPTuning {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state.Settings
}

// SetSettings applies a setting from the panel and reports whether it
// changed. A new percentage drops earlier adaptive expansions.
func (t *TCPMemoryTuner) SetSettings(settings domain.TCPTuning) bool {
	settings = normalizeTCPTuning(settings)
	t.mu.Lock()
	defer t.mu.Unlock()
	previous := t.state.Settings
	if previous == settings {
		return false
	}
	t.state.Settings = settings
	if previous.Enabled != settings.Enabled {
		if settings.Enabled {
			t.event("info", "Автоматика памяти TCP включена")
		} else {
			t.event("info", "Автоматика памяти TCP выключена: текущие лимиты оставлены как есть")
		}
	}
	if previous.MemoryPercent != settings.MemoryPercent {
		t.event("info", fmt.Sprintf("Лимит памяти TCP изменён в панели: %d%% → %d%% RAM", previous.MemoryPercent, settings.MemoryPercent))
		t.state.Adaptive = 0
	}
	t.save()
	return true
}

// Tick re-detects RAM, vCPUs and TCP memory, adjusts tcp_mem when the
// automation is enabled and returns the status for the panel.
func (t *TCPMemoryTuner) Tick() domain.TCPMemoryStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	status := domain.TCPMemoryStatus{Automatic: t.state.Settings.Enabled, Percent: t.state.Settings.MemoryPercent, CheckedAt: t.now().UTC()}
	total, available, err := readMemInfo(meminfoPath)
	if err != nil {
		status.Warning = "Не удалось прочитать объём RAM: " + err.Error()
		return t.finish(status)
	}
	cpus := 0
	if list, cpuErr := onlineCPUs(); cpuErr == nil {
		cpus = len(list)
	}
	if t.ramBytes != 0 && total != t.ramBytes {
		t.event("info", fmt.Sprintf("Объём RAM изменился: %s → %s", formatMemory(t.ramBytes), formatMemory(total)))
	}
	if t.cpus != 0 && cpus != 0 && cpus != t.cpus {
		t.event("info", fmt.Sprintf("Число vCPU изменилось: %d → %d", t.cpus, cpus))
		if t.onCPUChange != nil {
			t.onCPUChange()
		}
	}
	t.ramBytes = total
	if cpus != 0 {
		t.cpus = cpus
	}
	status.RAMBytes, status.AvailableBytes, status.CPUs = total, available, cpus

	limits, err := readTCPMem(tcpMemPath)
	if err != nil {
		// Containers and LXC have their own network namespace without the
		// host-wide tcp_mem; the host's value applies there.
		status.Warning = "tcp_mem недоступен на этой ноде (контейнер или LXC): лимит задаётся на хосте"
		return t.finish(status)
	}
	used := readSockstatTCPMem(sockstatPath)
	pressures := readNetstatCounter(netstatPath, "TcpExt", "TCPMemoryPressures")
	oom := readVMStatCounter(vmstatPath, "oom_kill")
	var pressureDelta, oomDelta uint64
	if t.sampled {
		if pressures >= t.lastPressures {
			pressureDelta = pressures - t.lastPressures
		}
		if oom >= t.lastOOM {
			oomDelta = oom - t.lastOOM
		}
	}
	t.lastPressures, t.lastOOM, t.sampled = pressures, oom, true
	if oomDelta > 0 {
		t.eventKey("warning", "oom", fmt.Sprintf("Ядро завершило процессы из-за нехватки памяти (OOM): %d", oomDelta))
	}

	page := t.pageSize
	reserve := max(uint64(tcpMemMinReserveBytes), total/100*tcpMemReservePercent)
	var ceiling uint64
	if total > reserve {
		ceiling = total - reserve
	}
	status.CeilingBytes, status.PressureEvents = ceiling, pressureDelta
	capped, exhausted := false, false
	if t.state.Settings.Enabled {
		capped, exhausted = t.adjust(total, available, ceiling, reserve, limits, used, pressureDelta, oomDelta)
		if current, readErr := readTCPMem(tcpMemPath); readErr == nil {
			limits = current
		}
	}
	for i, value := range limits {
		status.LimitBytes[i] = value * page
	}
	status.UsedBytes = used * page
	status.AdaptivePercent = t.state.Adaptive

	ceilingPercent := 0
	if total > 0 {
		ceilingPercent = int(ceiling * 100 / total)
	}
	switch {
	case pressureDelta > 0 && !t.state.Settings.Enabled:
		status.Warning = fmt.Sprintf("Ядру не хватает памяти TCP (%d событий за проверку). Автоматика выключена — увеличьте лимит.", pressureDelta)
	case pressureDelta > 0 && exhausted:
		status.Warning = fmt.Sprintf("Ядру не хватает памяти TCP (%d событий за проверку), а лимит уже упёрся в безопасный предел %d%% RAM. Ноде нужно больше RAM или меньше нагрузки.", pressureDelta, ceilingPercent)
	case pressureDelta > 0:
		status.Warning = fmt.Sprintf("Ядру не хватает памяти TCP (%d событий за проверку). Скорость клиентов может падать.", pressureDelta)
	case t.state.Settings.Enabled && capped && t.state.Adaptive == 0:
		status.Warning = fmt.Sprintf("Заданные %d%% RAM выше безопасного предела: применено %d%%, остальное — резерв для системы и HAProxy.", t.state.Settings.MemoryPercent, ceilingPercent)
	}
	return t.finish(status)
}

// adjust computes the target tcp_mem and writes it. It reports whether the
// target was cut to the RAM ceiling and whether another expansion step would
// no longer fit under it.
func (t *TCPMemoryTuner) adjust(total, available, ceiling, reserve uint64, current [3]uint64, used, pressureDelta, oomDelta uint64) (bool, bool) {
	page := t.pageSize
	base := t.state.Settings.MemoryPercent
	// Adaptive expansion: the kernel was under TCP memory pressure during
	// the last interval and usage sits near the pressure threshold.
	if pressureDelta > 0 && used*10 >= current[1]*9 {
		next := base + t.state.Adaptive + tcpMemAdaptiveStep
		nextMax := total / 100 * uint64(next)
		growth := nextMax - min(nextMax, current[2]*page)
		psi := readPSISomeAvg10(psiMemoryPath)
		switch {
		case oomDelta > 0:
			t.event("warning", "Лимит TCP не расширен: были завершения процессов из-за нехватки памяти (OOM)")
		case psi >= tcpMemPSILimit:
			t.eventKey("warning", "psi", fmt.Sprintf("Лимит TCP не расширен: система под давлением памяти (PSI %.0f%%)", psi))
		case nextMax > ceiling:
			// Already at the ceiling; Tick reports it as a warning.
		case available < reserve+growth:
			t.eventKey("warning", "low-available", fmt.Sprintf("Лимит TCP не расширен: свободно %s RAM, а резерв для системы %s", formatMemory(available), formatMemory(reserve)))
		default:
			t.state.Adaptive += tcpMemAdaptiveStep
			t.event("info", fmt.Sprintf("Давление памяти TCP: лимит расширен до %d%% RAM", next))
			t.save()
		}
	}
	targetMax := total / 100 * uint64(base+t.state.Adaptive)
	capped := targetMax > ceiling
	if capped {
		targetMax = ceiling
	}
	exhausted := capped || total/100*uint64(base+t.state.Adaptive+tcpMemAdaptiveStep) > ceiling
	target := tcpMemThresholds(targetMax / page)
	if target == current {
		return capped, exhausted
	}
	if target[2] < current[2] {
		if current != t.state.Applied {
			t.eventKey("info", "external", fmt.Sprintf("tcp_mem задан вручную (%s) выше автоматического значения — оставлен", t.formatLimits(current)))
			return capped, exhausted
		}
		// Lowering below what live connections already use would push
		// them straight into pressure.
		if used >= target[0] {
			t.eventKey("info", "lower-deferred", fmt.Sprintf("Снижение лимита TCP отложено: соединения используют %s", formatMemory(used*page)))
			return capped, exhausted
		}
	}
	value := fmt.Sprintf("%d %d %d", target[0], target[1], target[2])
	if err := os.WriteFile(tcpMemPath, []byte(value), 0); err != nil {
		t.event("warning", "Не удалось изменить tcp_mem: "+err.Error())
		return capped, exhausted
	}
	t.event("info", fmt.Sprintf("tcp_mem: %s → %s", t.formatLimits(current), t.formatLimits(target)))
	t.state.Applied = target
	t.save()
	return capped, exhausted
}

// tcpMemThresholds derives low and pressure from the hard limit (in pages):
// 5/7 and 6/7 of it, i.e. 50% and 60% of RAM for the default 70%.
func tcpMemThresholds(maximum uint64) [3]uint64 {
	return [3]uint64{maximum * 5 / 7, maximum * 6 / 7, maximum}
}

func (t *TCPMemoryTuner) finish(status domain.TCPMemoryStatus) domain.TCPMemoryStatus {
	status.Events = append([]domain.TCPMemoryEvent(nil), t.events...)
	return status
}

func (t *TCPMemoryTuner) event(level, message string) { t.eventKey(level, message, message) }

// eventKey records an event unless one with the same key was recorded within
// tcpMemEventRepeat; the key leaves out numbers that change every check.
func (t *TCPMemoryTuner) eventKey(level, key, message string) {
	now := t.now().UTC()
	if seen, ok := t.eventSeen[key]; ok && now.Sub(seen) < tcpMemEventRepeat {
		return
	}
	t.eventSeen[key] = now
	if level == "warning" {
		t.logger.Warn("tcp memory: "+message)
	} else {
		t.logger.Info("tcp memory: "+message)
	}
	t.events = append(t.events, domain.TCPMemoryEvent{At: now, Level: level, Message: message})
	if len(t.events) > tcpMemEventLimit {
		t.events = append([]domain.TCPMemoryEvent(nil), t.events[len(t.events)-tcpMemEventLimit:]...)
	}
}

func (t *TCPMemoryTuner) save() {
	data, err := json.Marshal(t.state)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0750); err != nil {
		t.logger.Warn("save TCP tuning state", "error", err)
		return
	}
	tmp := t.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0640); err == nil {
		err = os.Rename(tmp, t.path)
		if err != nil {
			t.logger.Warn("save TCP tuning state", "error", err)
		}
	}
}

func (t *TCPMemoryTuner) formatLimits(pages [3]uint64) string {
	return fmt.Sprintf("%s / %s / %s", formatMemory(pages[0]*t.pageSize), formatMemory(pages[1]*t.pageSize), formatMemory(pages[2]*t.pageSize))
}

func formatMemory(bytes uint64) string {
	if bytes >= 1<<30 {
		return strconv.FormatFloat(float64(bytes)/(1<<30), 'f', 1, 64) + " ГБ"
	}
	return strconv.FormatUint(bytes>>20, 10) + " МБ"
}

// readMemInfo returns MemTotal and MemAvailable in bytes.
func readMemInfo(path string) (uint64, uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	var total, available uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = value * 1024
		case "MemAvailable:":
			available = value * 1024
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	if total == 0 {
		return 0, 0, fmt.Errorf("MemTotal missing in %s", path)
	}
	return total, available, nil
}

func readTCPMem(path string) ([3]uint64, error) {
	var result [3]uint64
	data, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	fields := strings.Fields(string(data))
	if len(fields) != 3 {
		return result, errors.New("unexpected tcp_mem format")
	}
	for i, field := range fields {
		if result[i], err = strconv.ParseUint(field, 10, 64); err != nil {
			return result, err
		}
	}
	return result, nil
}

// readSockstatTCPMem returns the pages TCP buffers use ("TCP: ... mem N").
func readSockstatTCPMem(path string) uint64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "TCP:" {
			continue
		}
		for i := 1; i+1 < len(fields); i += 2 {
			if fields[i] == "mem" {
				value, _ := strconv.ParseUint(fields[i+1], 10, 64)
				return value
			}
		}
	}
	return 0
}

// readNetstatCounter reads one counter of /proc/net/netstat, where each
// group is a header line of names followed by a line of values.
func readNetstatCounter(path, group, name string) uint64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	lines := strings.Split(string(data), "\n")
	for i := 0; i+1 < len(lines); i++ {
		names, values := strings.Fields(lines[i]), strings.Fields(lines[i+1])
		if len(names) == 0 || names[0] != group+":" || len(values) != len(names) || values[0] != group+":" {
			continue
		}
		for j := 1; j < len(names); j++ {
			if names[j] == name {
				value, _ := strconv.ParseUint(values[j], 10, 64)
				return value
			}
		}
	}
	return 0
}

func readVMStatCounter(path, name string) uint64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if key, value, found := strings.Cut(line, " "); found && key == name {
			parsed, _ := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			return parsed
		}
	}
	return 0
}

// readPSISomeAvg10 returns memory PSI "some avg10", 0 when PSI is missing.
func readPSISomeAvg10(path string) float64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "some" {
			continue
		}
		if value, found := strings.CutPrefix(fields[1], "avg10="); found {
			parsed, _ := strconv.ParseFloat(value, 64)
			return parsed
		}
	}
	return 0
}
