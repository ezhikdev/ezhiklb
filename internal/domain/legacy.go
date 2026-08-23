package domain

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ParseLegacyFile converts the v2 Ezhik UDP INI-like configuration into the
// first reusable EzhikLB profile. It intentionally does not modify the source.
func ParseLegacyFile(path string) (ProfileConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return ProfileConfig{}, err
	}
	defer file.Close()

	config := DefaultProfileConfig()
	config.HealthCheck.Enabled = true
	var current *Listener
	globalAffinity := 0
	globalScheduler := "wrr"

	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			if strings.EqualFold(name, "GLOBAL") {
				current = nil
				continue
			}
			config.Listeners = append(config.Listeners, Listener{
				ID: "legacy_" + strings.ToLower(name), Name: name, Enabled: true,
				ListenAddress: "0.0.0.0", Protocols: []Protocol{ProtocolUDP}, Scheduler: globalScheduler, AffinitySecs: globalAffinity,
			})
			current = &config.Listeners[len(config.Listeners)-1]
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			return ProfileConfig{}, fmt.Errorf("legacy line %d: expected KEY=VALUE", lineNumber)
		}
		key, value = strings.ToUpper(strings.TrimSpace(key)), strings.TrimSpace(value)
		if current == nil {
			switch key {
			case "PERSISTENCE":
				globalAffinity, err = strconv.Atoi(value)
			case "SCHEDULER":
				globalScheduler = strings.ToLower(value)
			}
			if err != nil {
				return ProfileConfig{}, fmt.Errorf("legacy line %d: %w", lineNumber, err)
			}
			continue
		}
		switch key {
		case "PORT":
			port, parseErr := strconv.ParseUint(value, 10, 16)
			if parseErr != nil {
				return ProfileConfig{}, fmt.Errorf("legacy line %d: %w", lineNumber, parseErr)
			}
			current.ListenPort = uint16(port)
			for i := range current.Backends {
				current.Backends[i].Port = uint16(port)
			}
		case "ENABLED":
			current.Enabled = value == "1" || strings.EqualFold(value, "true") || strings.EqualFold(value, "yes")
		case "PERSISTENCE":
			current.AffinitySecs, err = strconv.Atoi(value)
		case "SCHEDULER":
			current.Scheduler = strings.ToLower(value)
		case "SERVER":
			fields := strings.Fields(value)
			if len(fields) == 0 {
				return ProfileConfig{}, fmt.Errorf("legacy line %d: empty SERVER", lineNumber)
			}
			weight := 1
			for _, field := range fields[1:] {
				if raw, ok := strings.CutPrefix(strings.ToUpper(field), "WEIGHT="); ok {
					weight, err = strconv.Atoi(raw)
				}
			}
			current.Backends = append(current.Backends, Backend{ID: fmt.Sprintf("legacy_%s_%d", strings.ToLower(current.Name), len(current.Backends)+1), Address: fields[0], Port: current.ListenPort, Weight: weight, Enabled: true})
		}
		if err != nil {
			return ProfileConfig{}, fmt.Errorf("legacy line %d: %w", lineNumber, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return ProfileConfig{}, err
	}
	// Legacy sections can declare SERVER before PORT. Normalize after parsing.
	for i := range config.Listeners {
		listener := &config.Listeners[i]
		if listener.Scheduler == "" {
			listener.Scheduler = globalScheduler
		}
		for j := range listener.Backends {
			if listener.Backends[j].Port == 0 {
				listener.Backends[j].Port = listener.ListenPort
			}
		}
	}
	if err := config.Validate(); err != nil {
		return ProfileConfig{}, fmt.Errorf("legacy configuration is invalid: %w", err)
	}
	return config, nil
}

