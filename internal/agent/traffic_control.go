package agent

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
)

const (
	rateMarkMask   uint32 = 0xffff0000
	rateMarkBase   uint32 = 0xe0000000
	maxRateSlots          = 2047
	rateFilterBase        = 40000
)

type TrafficControl struct {
	ListenerID string `json:"listener_id"`
	Direction  string `json:"direction"`
	Device     string `json:"device"`
	Preference int    `json:"preference"`
	Mark       uint32 `json:"mark"`
	RateMbps   int    `json:"rate_mbps"`
}

type limitedListener struct {
	id           string
	address      string
	port         uint16
	rateMbps     int
	destinations []Destination
}

func hasRateLimitedServices(services []Service) bool {
	for _, service := range services {
		if service.RateLimitEnabled && service.RateLimitMbps > 0 {
			return true
		}
	}
	return false
}

func (r *Reconciler) reconcileTrafficControl(ctx context.Context, old []TrafficControl, services []Service) ([]TrafficControl, error) {
	if len(old) == 0 && !hasRateLimitedServices(services) {
		// The normal upgrade path deliberately stops here: nodes whose profiles
		// do not opt in never receive a mutating tc or mangle command.
		return nil, nil
	}
	desired, err := r.buildTrafficControls(ctx, services)
	if err != nil {
		return nil, err
	}
	for _, control := range desired {
		if err := r.ensureClsact(ctx, control.Device); err != nil {
			return desired, err
		}
		burst := control.RateMbps * 1250 // 10 ms of traffic at the configured rate.
		if burst < 65536 {
			burst = 65536
		}
		if burst > 4<<20 {
			burst = 4 << 20
		}
		args := []string{"filter", "replace", "dev", control.Device, "egress", "protocol", "ip", "pref", strconv.Itoa(control.Preference), "handle", markSpec(control.Mark), "fw", "action", "police", "rate", fmt.Sprintf("%dmbit", control.RateMbps), "burst", fmt.Sprintf("%db", burst), "conform-exceed", "drop/pass"}
		if _, err := r.runner.Run(ctx, "tc", args, ""); err != nil {
			return desired, fmt.Errorf("install %s limiter for listener %s on %s: %w", control.Direction, control.ListenerID, control.Device, err)
		}
	}
	if len(desired) == 0 {
		if err := r.removeTrafficMarks(ctx); err != nil {
			return desired, err
		}
	} else if err := r.applyTrafficMarks(ctx, services, desired); err != nil {
		return desired, err
	}
	desiredKeys := map[string]bool{}
	for _, control := range desired {
		desiredKeys[trafficControlKey(control)] = true
	}
	for _, control := range old {
		if desiredKeys[trafficControlKey(control)] {
			continue
		}
		args := []string{"filter", "delete", "dev", control.Device, "egress", "protocol", "ip", "pref", strconv.Itoa(control.Preference), "handle", markSpec(control.Mark), "fw"}
		_, _ = r.runner.Run(ctx, "tc", args, "")
	}
	return desired, nil
}

func (r *Reconciler) buildTrafficControls(ctx context.Context, services []Service) ([]TrafficControl, error) {
	listeners := map[string]*limitedListener{}
	for _, service := range services {
		if !service.RateLimitEnabled || service.RateLimitMbps <= 0 {
			continue
		}
		item := listeners[service.ListenerID]
		if item == nil {
			item = &limitedListener{id: service.ListenerID, address: service.Address, port: service.Port, rateMbps: service.RateLimitMbps}
			listeners[service.ListenerID] = item
		}
		item.destinations = append(item.destinations, service.Destinations...)
	}
	ids := make([]string, 0, len(listeners))
	for id := range listeners {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > maxRateSlots {
		return nil, fmt.Errorf("too many rate-limited listeners: %d", len(ids))
	}
	slots := allocateRateSlots(ids)
	clientDevice := ""
	if len(ids) > 0 {
		var err error
		clientDevice, err = r.routeDevice(ctx, "1.1.1.1")
		if err != nil {
			return nil, fmt.Errorf("resolve client egress interface: %w", err)
		}
	}
	result := make([]TrafficControl, 0, len(ids)*2)
	for _, id := range ids {
		listener := listeners[id]
		backendDevices := map[string]bool{}
		for _, destination := range listener.destinations {
			device, err := r.routeDevice(ctx, destination.Address)
			if err != nil {
				return nil, fmt.Errorf("resolve backend egress interface for %s: %w", destination.Address, err)
			}
			backendDevices[device] = true
		}
		if len(backendDevices) != 1 {
			return nil, fmt.Errorf("listener %s rate limit requires all enabled backends to use one egress interface", id)
		}
		backendDevice := ""
		for device := range backendDevices {
			backendDevice = device
		}
		slot := slots[id]
		// Reserve the upper nibble for EzhikLB and encode direction in the
		// low bit of the remaining 12-bit slot. The mask preserves any mark
		// bits owned by another packet-processing component.
		baseMark := rateMarkBase | uint32(slot*2)<<16
		result = append(result,
			TrafficControl{ListenerID: id, Direction: "original", Device: backendDevice, Preference: rateFilterBase + slot*2, Mark: baseMark, RateMbps: listener.rateMbps},
			TrafficControl{ListenerID: id, Direction: "reply", Device: clientDevice, Preference: rateFilterBase + slot*2 + 1, Mark: baseMark | 1<<16, RateMbps: listener.rateMbps},
		)
	}
	return result, nil
}

func allocateRateSlots(ids []string) map[string]int {
	result := make(map[string]int, len(ids))
	used := map[int]bool{}
	for _, id := range ids {
		hash := fnv.New32a()
		_, _ = hash.Write([]byte(id))
		slot := int(hash.Sum32()%maxRateSlots) + 1
		for used[slot] {
			slot++
			if slot > maxRateSlots {
				slot = 1
			}
		}
		used[slot] = true
		result[id] = slot
	}
	return result
}

func (r *Reconciler) routeDevice(ctx context.Context, address string) (string, error) {
	output, err := r.runner.Run(ctx, "ip", []string{"-4", "route", "get", address}, "")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(output)
	for index, field := range fields {
		if field == "dev" && index+1 < len(fields) && fields[index+1] != "" {
			return fields[index+1], nil
		}
	}
	return "", fmt.Errorf("route to %s has no output device: %s", address, strings.TrimSpace(output))
}

func (r *Reconciler) ensureClsact(ctx context.Context, device string) error {
	output, err := r.runner.Run(ctx, "tc", []string{"qdisc", "show", "dev", device}, "")
	if err != nil {
		return fmt.Errorf("inspect qdisc on %s: %w", device, err)
	}
	if strings.Contains(output, "qdisc clsact ") {
		return nil
	}
	if _, err := r.runner.Run(ctx, "tc", []string{"qdisc", "add", "dev", device, "clsact"}, ""); err != nil {
		return fmt.Errorf("add non-invasive clsact qdisc on %s: %w", device, err)
	}
	return nil
}

func (r *Reconciler) applyTrafficMarks(ctx context.Context, services []Service, controls []TrafficControl) error {
	marks := map[string]map[string]uint32{}
	for _, control := range controls {
		if marks[control.ListenerID] == nil {
			marks[control.ListenerID] = map[string]uint32{}
		}
		marks[control.ListenerID][control.Direction] = control.Mark
	}
	rules := []string{"*mangle", ":EZHIKLB-RATE - [0:0]", "-F EZHIKLB-RATE"}
	for _, service := range services {
		if !service.RateLimitEnabled || service.RateLimitMbps <= 0 {
			continue
		}
		for _, direction := range []string{"ORIGINAL", "REPLY"} {
			mark := marks[service.ListenerID][strings.ToLower(direction)]
			rules = append(rules, fmt.Sprintf("-A EZHIKLB-RATE -m ipvs --ipvs --vproto %s --vaddr %s --vport %d --vdir %s -j MARK --set-xmark %s", service.Protocol, service.Address, service.Port, direction, markSpec(mark)))
		}
	}
	rules = append(rules, "COMMIT", "")
	if _, err := r.runner.Run(ctx, "iptables-restore", []string{"--noflush"}, strings.Join(rules, "\n")); err != nil {
		return fmt.Errorf("apply rate-limit packet marks: %w", err)
	}
	if err := r.ensureJump(ctx, "mangle", "POSTROUTING", "EZHIKLB-RATE"); err != nil {
		return fmt.Errorf("attach rate-limit packet marks: %w", err)
	}
	return nil
}

func (r *Reconciler) removeTrafficMarks(ctx context.Context) error {
	prefix := []string{"-w", "5", "-t", "mangle"}
	check := append(append([]string{}, prefix...), "-C", "POSTROUTING", "-j", "EZHIKLB-RATE")
	if _, err := r.runner.Run(ctx, "iptables", check, ""); err == nil {
		remove := append(append([]string{}, prefix...), "-D", "POSTROUTING", "-j", "EZHIKLB-RATE")
		if _, err := r.runner.Run(ctx, "iptables", remove, ""); err != nil {
			return err
		}
	}
	_, _ = r.runner.Run(ctx, "iptables", append(append([]string{}, prefix...), "-F", "EZHIKLB-RATE"), "")
	_, _ = r.runner.Run(ctx, "iptables", append(append([]string{}, prefix...), "-X", "EZHIKLB-RATE"), "")
	return nil
}

func trafficControlKey(control TrafficControl) string {
	return fmt.Sprintf("%s/%d/%08x", control.Device, control.Preference, control.Mark)
}

func markSpec(mark uint32) string {
	return fmt.Sprintf("0x%08x/0x%08x", mark, rateMarkMask)
}
