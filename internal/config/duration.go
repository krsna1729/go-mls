package config

import (
	"encoding/json"
	"fmt"
	"time"
)

// UnmarshalJSON parses a duration from a JSON string (e.g., "30s").
func UnmarshalJSON(b []byte) (time.Duration, error) {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("invalid duration string: %w", err)
		}
		return parsed, nil
	}
	// Fallback: try to unmarshal as a number (seconds)
	var n int64
	if err := json.Unmarshal(b, &n); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	return 0, fmt.Errorf("invalid duration format: %s", string(b))
}

// MarshalJSON outputs the duration as a string (e.g., "30s").
func MarshalJSON(d time.Duration) ([]byte, error) {
	return json.Marshal(d.String())
}

// String returns the duration as a string (e.g., "30s").
func String(d time.Duration) string {
	return d.String()
}
