package config

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDuration_UnmarshalJSON_String(t *testing.T) {
	var d time.Duration
	input := `"30s"`
	var err error
	d, err = UnmarshalJSON([]byte(input))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if d != 30*time.Second {
		t.Errorf("expected 30s, got %v", d)
	}
}

func TestDuration_UnmarshalJSON_Number(t *testing.T) {
	var d time.Duration
	input := `60`
	var err error
	d, err = UnmarshalJSON([]byte(input))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if d != 60*time.Second {
		t.Errorf("expected 60s, got %v", d)
	}
}

func TestDuration_UnmarshalJSON_Invalid(t *testing.T) {
	_, err := UnmarshalJSON([]byte(`"notaduration"`))
	if err == nil {
		t.Error("expected error for invalid duration string, got nil")
	}
	_, err = UnmarshalJSON([]byte(`{"foo":1}`))
	if err == nil {
		t.Error("expected error for invalid duration format, got nil")
	}
}

func TestDuration_MarshalJSON(t *testing.T) {
	d := 45 * time.Second
	b, err := MarshalJSON(d)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("expected valid JSON string, got %v", err)
	}
	if s != "45s" {
		t.Errorf("expected '45s', got '%s'", s)
	}
}

func TestDuration_String(t *testing.T) {
	d := 90 * time.Second
	if got := String(d); got != "1m30s" {
		t.Errorf("expected '1m30s', got '%s'", got)
	}
}
