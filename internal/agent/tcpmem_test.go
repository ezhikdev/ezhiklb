package agent

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ezhik-lb/ezhiklb/internal/domain"
)

const testGiB = uint64(1) << 30

// fakeProc points the tuner's /proc and /sys paths at a temporary directory.
type fakeProc struct {
	t   *testing.T
	dir string
}

func newFakeProc(t *testing.T) *fakeProc {
	dir := t.TempDir()
	saved := []*string{&tcpMemPath, &sockstatPath, &netstatPath, &meminfoPath, &vmstatPath, &psiMemoryPath, &cpuOnlinePath}
	values := make([]string, len(saved))
	for i, pointer := range saved {
		values[i] = *pointer
	}
	t.Cleanup(func() {
		for i, pointer := range saved {
			*pointer = values[i]
		}
	})
	tcpMemPath = filepath.Join(dir, "tcp_mem")
	sockstatPath = filepath.Join(dir, "sockstat")
	netstatPath = filepath.Join(dir, "netstat")
	meminfoPath = filepath.Join(dir, "meminfo")
	vmstatPath = filepath.Join(dir, "vmstat")
	psiMemoryPath = filepath.Join(dir, "psi")
	cpuOnlinePath = filepath.Join(dir, "cpu_online")
	p := &fakeProc{t: t, dir: dir}
	p.memory(4*testGiB, 3*testGiB)
	p.tcpMem(44334, 59113, 88668)
	p.used(1000)
	p.pressures(0)
	p.oom(0)
	p.psi(0)
	p.cpus("0-1")
	return p
}

func (p *fakeProc) write(path, data string) {
	p.t.Helper()
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		p.t.Fatal(err)
	}
}

func (p *fakeProc) memory(total, available uint64) {
	p.write(meminfoPath, fmt.Sprintf("MemTotal:       %d kB\nMemFree:        1 kB\nMemAvailable:   %d kB\n", total/1024, available/1024))
}
func (p *fakeProc) tcpMem(low, pressure, high uint64) {
	p.write(tcpMemPath, fmt.Sprintf("%d\t%d\t%d\n", low, pressure, high))
}
func (p *fakeProc) used(pages uint64) {
	p.write(sockstatPath, fmt.Sprintf("sockets: used 100\nTCP: inuse 49104 orphan 2226 tw 16315 alloc 49620 mem %d\nUDP: inuse 3 mem 2\n", pages))
}
func (p *fakeProc) pressures(count uint64) {
	p.write(netstatPath, fmt.Sprintf("TcpExt: SyncookiesSent TCPMemoryPressures PruneCalled\nTcpExt: 0 %d 17830465\nIpExt: InNoRoutes\nIpExt: 0\n", count))
}
func (p *fakeProc) oom(count uint64)     { p.write(vmstatPath, fmt.Sprintf("nr_free_pages 100\noom_kill %d\n", count)) }
func (p *fakeProc) psi(avg10 float64)    { p.write(psiMemoryPath, fmt.Sprintf("some avg10=%.2f avg60=0.00 avg300=0.00 total=1\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n", avg10)) }
func (p *fakeProc) cpus(list string)     { p.write(cpuOnlinePath, list+"\n") }
func (p *fakeProc) current() [3]uint64 {
	p.t.Helper()
	limits, err := readTCPMem(tcpMemPath)
	if err != nil {
		p.t.Fatal(err)
	}
	return limits
}

func newTestTuner(t *testing.T, dir string, onCPUChange func()) *TCPMemoryTuner {
	tuner := NewTCPMemoryTuner(slog.New(slog.NewTextHandler(io.Discard, nil)), filepath.Join(dir, "state", "tcp-tuning.json"), onCPUChange)
	tuner.pageSize = 4096
	return tuner
}

// pagesFor is the tcp_mem the tuner derives from percent of RAM.
func pagesFor(total uint64, percent int) [3]uint64 {
	return tcpMemThresholds(total / 100 * uint64(percent) / 4096)
}

func hasEvent(status domain.TCPMemoryStatus, part string) bool {
	for _, event := range status.Events {
		if strings.Contains(event.Message, part) {
			return true
		}
	}
	return false
}

func TestTCPMemoryRaisesKernelDefaultToConfiguredPercent(t *testing.T) {
	proc := newFakeProc(t)
	tuner := newTestTuner(t, proc.dir, nil)
	status := tuner.Tick()
	want := pagesFor(4*testGiB, 70)
	if got := proc.current(); got != want {
		t.Fatalf("tcp_mem = %v, want %v (70%% of RAM)", got, want)
	}
	if !status.Automatic || status.Percent != 70 || status.LimitBytes[2] != want[2]*4096 || status.RAMBytes != 4*testGiB || status.CPUs != 2 {
		t.Fatalf("status = %+v", status)
	}
	if !hasEvent(status, "tcp_mem:") || status.Warning != "" {
		t.Fatalf("expected a change event and no warning: %+v", status)
	}
	// Low and pressure are 50% and 60% of RAM for the default 70%.
	if want[0]*4096 < 4*testGiB/100*49 || want[1]*4096 < 4*testGiB/100*59 {
		t.Fatalf("thresholds %v are not 50%%/60%% of RAM", want)
	}
}

func TestTCPMemoryDisabledLeavesLimits(t *testing.T) {
	proc := newFakeProc(t)
	tuner := newTestTuner(t, proc.dir, nil)
	tuner.SetSettings(domain.TCPTuning{Enabled: false, MemoryPercent: 70})
	tuner.Tick()
	proc.pressures(5)
	status := tuner.Tick()
	if got := proc.current(); got != [3]uint64{44334, 59113, 88668} {
		t.Fatalf("disabled automation changed tcp_mem to %v", got)
	}
	if !strings.Contains(status.Warning, "Автоматика выключена") || status.PressureEvents != 5 {
		t.Fatalf("pressure with automation off must warn: %+v", status)
	}
}

func TestTCPMemoryAdaptiveExpansion(t *testing.T) {
	proc := newFakeProc(t)
	tuner := newTestTuner(t, proc.dir, nil)
	tuner.Tick()
	limits := proc.current()
	// The kernel enters pressure with usage at the pressure threshold.
	proc.used(limits[1])
	proc.pressures(40)
	status := tuner.Tick()
	if status.AdaptivePercent != 5 || proc.current() != pagesFor(4*testGiB, 75) {
		t.Fatalf("expected one 5%% step to 75%%: adaptive=%d tcp_mem=%v", status.AdaptivePercent, proc.current())
	}
	if !hasEvent(status, "расширен до 75% RAM") || !strings.Contains(status.Warning, "не хватает памяти TCP") {
		t.Fatalf("status = %+v", status)
	}
	// Further pressure grows it up to the ceiling (85% with a 15% reserve).
	for i := 0; i < 5; i++ {
		proc.used(proc.current()[1])
		proc.pressures(uint64(80 + i*40))
		status = tuner.Tick()
	}
	if status.AdaptivePercent != 15 || proc.current() != pagesFor(4*testGiB, 85) {
		t.Fatalf("expansion must stop at the RAM ceiling: adaptive=%d tcp_mem=%v", status.AdaptivePercent, proc.current())
	}
	if !strings.Contains(status.Warning, "безопасный предел 85% RAM") {
		t.Fatalf("pressure at the ceiling must say so: %q", status.Warning)
	}
}

func TestTCPMemoryExpansionBlocked(t *testing.T) {
	cases := map[string]func(*fakeProc){
		"OOM kill":           func(p *fakeProc) { p.oom(1) },
		"memory PSI":         func(p *fakeProc) { p.psi(25) },
		"low MemAvailable":   func(p *fakeProc) { p.memory(4*testGiB, 600<<20) },
		"usage far from cap": func(p *fakeProc) { p.used(1000) },
	}
	for name, block := range cases {
		t.Run(name, func(t *testing.T) {
			proc := newFakeProc(t)
			tuner := newTestTuner(t, proc.dir, nil)
			tuner.Tick()
			proc.used(proc.current()[1])
			proc.pressures(10)
			block(proc)
			status := tuner.Tick()
			if status.AdaptivePercent != 0 || proc.current() != pagesFor(4*testGiB, 70) {
				t.Fatalf("limit expanded despite %s: %+v", name, status)
			}
		})
	}
}

func TestTCPMemoryPercentAboveCeilingIsCapped(t *testing.T) {
	proc := newFakeProc(t)
	tuner := newTestTuner(t, proc.dir, nil)
	tuner.SetSettings(domain.TCPTuning{Enabled: true, MemoryPercent: 90})
	status := tuner.Tick()
	ceiling := 4*testGiB - 4*testGiB/100*15
	if got := proc.current(); got != tcpMemThresholds(ceiling/4096) {
		t.Fatalf("tcp_mem = %v, want the 85%% ceiling", got)
	}
	if !strings.Contains(status.Warning, "выше безопасного предела") {
		t.Fatalf("warning = %q", status.Warning)
	}
	// A 1 GiB node keeps at least 512 MiB for the system.
	proc.memory(testGiB, 800<<20)
	tuner.Tick()
	if got := proc.current()[2] * 4096; got > testGiB-512<<20 {
		t.Fatalf("1 GiB node: hard limit %d exceeds RAM minus 512 MiB", got)
	}
}

func TestTCPMemoryNeverLowersUnderLoadOrForeignValues(t *testing.T) {
	proc := newFakeProc(t)
	tuner := newTestTuner(t, proc.dir, nil)
	tuner.Tick()
	high := proc.current()
	// The administrator lowers the percentage while connections use more
	// than the new low threshold: the change waits.
	tuner.SetSettings(domain.TCPTuning{Enabled: true, MemoryPercent: 40})
	proc.used(high[0])
	status := tuner.Tick()
	if proc.current() != high || !hasEvent(status, "Снижение лимита TCP отложено") {
		t.Fatalf("limit lowered under load: %v", proc.current())
	}
	// Once usage drops below the new low threshold it is applied.
	proc.used(1000)
	tuner.Tick()
	if proc.current() != pagesFor(4*testGiB, 40) {
		t.Fatalf("tcp_mem = %v, want 40%%", proc.current())
	}
	// A larger value set by hand is never lowered.
	proc.tcpMem(500000, 600000, 700000)
	status = tuner.Tick()
	if proc.current() != [3]uint64{500000, 600000, 700000} || !hasEvent(status, "задан вручную") {
		t.Fatalf("manual tcp_mem was overwritten: %v", proc.current())
	}
}

func TestTCPMemoryDetectsResizeWithoutReboot(t *testing.T) {
	proc := newFakeProc(t)
	retuned := 0
	tuner := newTestTuner(t, proc.dir, func() { retuned++ })
	tuner.Tick()
	proc.memory(8*testGiB, 7*testGiB)
	proc.cpus("0-3")
	status := tuner.Tick()
	if retuned != 1 || status.CPUs != 4 || status.RAMBytes != 8*testGiB {
		t.Fatalf("resize not detected: retuned=%d status=%+v", retuned, status)
	}
	if !hasEvent(status, "Объём RAM изменился: 4.0 ГБ → 8.0 ГБ") || !hasEvent(status, "Число vCPU изменилось: 2 → 4") {
		t.Fatalf("events = %+v", status.Events)
	}
	if proc.current() != pagesFor(8*testGiB, 70) {
		t.Fatalf("tcp_mem not resized for 8 GiB: %v", proc.current())
	}
}

func TestTCPMemorySettingsPersistAndEventsDeduplicate(t *testing.T) {
	proc := newFakeProc(t)
	tuner := newTestTuner(t, proc.dir, nil)
	if !tuner.SetSettings(domain.TCPTuning{Enabled: false, MemoryPercent: 55}) || tuner.SetSettings(domain.TCPTuning{Enabled: false, MemoryPercent: 55}) {
		t.Fatal("SetSettings must report only real changes")
	}
	reloaded := newTestTuner(t, proc.dir, nil)
	if reloaded.Settings() != (domain.TCPTuning{Enabled: false, MemoryPercent: 55}) {
		t.Fatalf("settings not persisted: %+v", reloaded.Settings())
	}
	clock := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	tuner.now = func() time.Time { return clock }
	tuner.event("warning", "same")
	clock = clock.Add(time.Minute)
	tuner.event("warning", "same")
	clock = clock.Add(tcpMemEventRepeat)
	tuner.event("warning", "same")
	count := 0
	for _, event := range tuner.events {
		if event.Message == "same" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("identical events within %s must be merged, got %d", tcpMemEventRepeat, count)
	}
}

func TestTCPMemoryMissingInContainer(t *testing.T) {
	proc := newFakeProc(t)
	if err := os.Remove(tcpMemPath); err != nil {
		t.Fatal(err)
	}
	status := newTestTuner(t, proc.dir, nil).Tick()
	if !strings.Contains(status.Warning, "контейнер") || status.RAMBytes != 4*testGiB {
		t.Fatalf("status = %+v", status)
	}
}

func TestProcCounterParsers(t *testing.T) {
	proc := newFakeProc(t)
	proc.pressures(406)
	if got := readNetstatCounter(netstatPath, "TcpExt", "TCPMemoryPressures"); got != 406 {
		t.Fatalf("TCPMemoryPressures = %d", got)
	}
	if got := readNetstatCounter(netstatPath, "TcpExt", "Missing"); got != 0 {
		t.Fatalf("missing counter = %d", got)
	}
	proc.used(45744)
	if got := readSockstatTCPMem(sockstatPath); got != 45744 {
		t.Fatalf("sockstat mem = %d", got)
	}
	proc.psi(12.5)
	if got := readPSISomeAvg10(psiMemoryPath); got != 12.5 {
		t.Fatalf("PSI = %v", got)
	}
}
