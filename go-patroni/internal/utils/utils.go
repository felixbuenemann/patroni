// Package utils provides utility functions used throughout Patroni.
package utils

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParseBool parses a boolean value from various formats.
func ParseBool(value interface{}) *bool {
	var result bool

	switch v := value.(type) {
	case bool:
		result = v
	case string:
		lower := strings.ToLower(v)
		switch lower {
		case "on", "true", "yes", "1":
			result = true
		case "off", "false", "no", "0":
			result = false
		default:
			return nil
		}
	case int, int64, float64:
		var num int64
		switch n := v.(type) {
		case int:
			num = int64(n)
		case int64:
			num = n
		case float64:
			num = int64(n)
		}
		result = num != 0
	default:
		return nil
	}

	return &result
}

// ParseInt parses an integer value with optional unit suffix.
func ParseInt(value interface{}) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		return int64(v), nil
	case string:
		return parseIntWithUnits(v)
	default:
		return 0, fmt.Errorf("cannot parse %T as int", value)
	}
}

// parseIntWithUnits parses an integer with optional memory/time unit suffix.
func parseIntWithUnits(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	// Memory units
	memoryUnits := map[string]int64{
		"B":  1,
		"KB": 1024,
		"K":  1024,
		"MB": 1024 * 1024,
		"M":  1024 * 1024,
		"GB": 1024 * 1024 * 1024,
		"G":  1024 * 1024 * 1024,
		"TB": 1024 * 1024 * 1024 * 1024,
		"T":  1024 * 1024 * 1024 * 1024,
	}

	// Time units
	timeUnits := map[string]int64{
		"US": 1,
		"MS": 1000,
		"S":  1000 * 1000,
		"M":  1000 * 1000 * 60,
		"H":  1000 * 1000 * 60 * 60,
		"D":  1000 * 1000 * 60 * 60 * 24,
	}

	// Try to extract number and unit
	re := regexp.MustCompile(`^(-?\d+)\s*([A-Za-z]*)$`)
	matches := re.FindStringSubmatch(s)
	if matches == nil {
		// Try parsing as plain number
		return strconv.ParseInt(s, 10, 64)
	}

	num, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return 0, err
	}

	unit := strings.ToUpper(matches[2])
	if unit == "" {
		return num, nil
	}

	if multiplier, ok := memoryUnits[unit]; ok {
		return num * multiplier, nil
	}
	if multiplier, ok := timeUnits[unit]; ok {
		return num * multiplier, nil
	}

	return num, nil
}

// DeepCompare recursively compares two maps.
func DeepCompare(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}

	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}

		aMap, aIsMap := av.(map[string]interface{})
		bMap, bIsMap := bv.(map[string]interface{})

		if aIsMap && bIsMap {
			if !DeepCompare(aMap, bMap) {
				return false
			}
		} else if fmt.Sprintf("%v", av) != fmt.Sprintf("%v", bv) {
			return false
		}
	}

	return true
}

// PatchConfig patches a config map with new values.
func PatchConfig(config, patch map[string]interface{}) bool {
	changed := false

	for k, v := range patch {
		if v == nil {
			if _, exists := config[k]; exists {
				delete(config, k)
				changed = true
			}
		} else if existingVal, exists := config[k]; exists {
			existingMap, existingIsMap := existingVal.(map[string]interface{})
			newMap, newIsMap := v.(map[string]interface{})

			if existingIsMap && newIsMap {
				if PatchConfig(existingMap, newMap) {
					changed = true
				}
			} else if fmt.Sprintf("%v", existingVal) != fmt.Sprintf("%v", v) {
				config[k] = v
				changed = true
			}
		} else {
			config[k] = v
			changed = true
		}
	}

	return changed
}

// SplitHostPort splits a host:port string.
func SplitHostPort(hostport string) (host, port string) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		// Assume it's just a host
		return hostport, ""
	}
	return host, port
}

// URI builds a URI from scheme and host:port.
func URI(scheme string, hostport string) string {
	return fmt.Sprintf("%s://%s", scheme, hostport)
}

// Retry implements retry logic with exponential backoff.
type Retry struct {
	MaxTries int
	Deadline time.Duration
	MaxDelay time.Duration
	attempts int
	deadline time.Time
}

// NewRetry creates a new Retry instance.
func NewRetry(maxTries int, deadline time.Duration, maxDelay time.Duration) *Retry {
	return &Retry{
		MaxTries: maxTries,
		Deadline: deadline,
		MaxDelay: maxDelay,
		deadline: time.Now().Add(deadline),
	}
}

// ShouldRetry returns true if another retry should be attempted.
func (r *Retry) ShouldRetry(err error) bool {
	if err == nil {
		return false
	}

	r.attempts++

	if r.MaxTries > 0 && r.attempts >= r.MaxTries {
		return false
	}

	if r.Deadline > 0 && time.Now().After(r.deadline) {
		return false
	}

	return true
}

// Delay returns the delay before the next retry.
func (r *Retry) Delay() time.Duration {
	delay := time.Duration(1<<uint(r.attempts-1)) * 100 * time.Millisecond
	if delay > r.MaxDelay {
		delay = r.MaxDelay
	}
	return delay
}

// PollingLoop implements a polling loop with sleep.
func PollingLoop(ctx context.Context, timeout time.Duration, interval time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-time.After(interval):
		}
	}
	return false
}
